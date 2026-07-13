package coordinator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/watch"
	virtv1 "kubevirt.io/api/core/v1"

	"github.com/stianfro/kvdrain/internal/kube"
	"github.com/stianfro/kvdrain/internal/state"
)

type EmitFunc func(state.Event) error

type Coordinator struct {
	Clients      kube.Clients
	Emit         EmitFunc
	Node         string
	RunID        string
	Now          func() time.Time
	PollInterval time.Duration
	emitErr      error
}

type DrainOptions struct {
	Timeout            time.Duration
	Retries            int
	ParallelOutbound   int
	Force              bool
	DeleteEmptyDirData bool
	GracePeriod        int64
	AbortUncordons     bool
}

func (c *Coordinator) init() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
}

func (c *Coordinator) event(typ, stateName, msg string, obj *state.ObjectRef, details map[string]any) {
	if c.Emit == nil {
		return
	}
	if err := c.Emit(state.Event{APIVersion: state.APIVersion, Kind: "Event", Time: c.Now().UTC(), RunID: c.RunID, Type: typ, Node: c.Node, Object: obj, State: stateName, Message: msg, Details: details}); err != nil && c.emitErr == nil {
		c.emitErr = err
	}
}

func (c *Coordinator) checkOutput() error {
	if c.emitErr != nil {
		return Operational("write output: %v", c.emitErr)
	}
	return nil
}

func ref(kind, ns, name, uid string) *state.ObjectRef {
	return &state.ObjectRef{Kind: kind, Namespace: ns, Name: name, UID: uid}
}

func (c *Coordinator) Status(ctx context.Context) error {
	c.init()
	if err := c.Clients.CheckStatusPermissions(ctx); err != nil {
		return Operational("status permissions: %v", err)
	}
	snapshot, err := c.Clients.Snapshot(ctx, c.Node)
	if err != nil {
		return Operational("status: %v", err)
	}
	blockers := 0
	for _, vmi := range snapshot.VMIs {
		status, message := "ready", "migratable"
		if vmi.Blocker != "" {
			status, message = "blocked", vmi.Blocker
			blockers++
		}
		c.event("vmi", status, message, ref("VirtualMachineInstance", vmi.VMI.Namespace, vmi.VMI.Name, string(vmi.VMI.UID)), map[string]any{
			"migratable":           vmi.Migratable,
			"migratableReason":     vmi.MigratableReason,
			"evictionStrategy":     vmi.EvictionStrategy,
			"eligibleTargets":      vmi.EligibleTargets,
			"nodeSelector":         vmi.VMI.Spec.NodeSelector,
			"requiredNodeAffinity": vmi.VMI.Spec.Affinity != nil && vmi.VMI.Spec.Affinity.NodeAffinity != nil && vmi.VMI.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil,
			"hotplugExpected":      vmi.HotplugExpected,
			"hotplugReady":         vmi.HotplugReady,
		})
	}
	for _, pod := range snapshot.Pods {
		if pod.Launcher || pod.Hotplug || pod.Ignored {
			continue
		}
		status, message := "evictable", "managed pod"
		blocked := false
		if pod.Blocker != "" {
			status, message = "blocked", pod.Blocker
			blocked = true
		}
		if pdbs := kube.BlockingPDBs(pod, true); len(pdbs) > 0 {
			status, message = "blocked", pdbMessage(pdbs)
			blocked = true
		}
		if blocked {
			blockers++
		}
		c.event("pod", status, message, ref("Pod", pod.Pod.Namespace, pod.Pod.Name, string(pod.Pod.UID)), map[string]any{"managed": pod.Managed, "emptyDir": pod.EmptyDir, "pdbs": pod.PDBs})
	}
	for _, migration := range snapshot.Migrations {
		c.emitMigration(migration, 0)
	}
	if err := c.checkOutput(); err != nil {
		return err
	}
	if blockers > 0 {
		return Operational("node %s has %d hard blocker(s)", c.Node, blockers)
	}
	c.event("node", "ready", "no hard blockers", ref("Node", "", c.Node, string(snapshot.Node.UID)), nil)
	return c.checkOutput()
}

func (c *Coordinator) Watch(ctx context.Context, nodes map[string]bool) error {
	c.init()
	if err := c.Clients.CheckWatchPermissions(ctx); err != nil {
		return Operational("watch permissions: %v", err)
	}
	tracked := map[string]kube.MigrationInfo{}
	for {
		listed, resourceVersion, err := c.Clients.ListMigrations(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return Interrupt("watch interrupted")
			}
			return Operational("watch: %v", err)
		}
		found := map[string]bool{}
		for _, migration := range listed {
			uid := string(migration.Migration.UID)
			found[uid] = true
			if migration.Active {
				tracked[uid] = migration
			}
			if _, ok := tracked[uid]; ok {
				c.emitWatchedMigration(migration, nodes)
				if !migration.Active {
					delete(tracked, uid)
				}
			}
		}
		for uid, migration := range tracked {
			if found[uid] {
				continue
			}
			migration.Active = false
			migration.State = "Deleted"
			migration.Reason = "migration object deleted before a terminal phase was observed"
			c.emitWatchedMigration(migration, nodes)
			delete(tracked, uid)
		}
		if err := c.checkOutput(); err != nil {
			return err
		}

		stream, err := c.Clients.WatchMigrations(ctx, resourceVersion)
		if err != nil {
			if ctx.Err() != nil {
				return Interrupt("watch interrupted")
			}
			return Operational("watch: %v", err)
		}
		for {
			select {
			case <-ctx.Done():
				stream.Stop()
				return Interrupt("watch interrupted")
			case event, ok := <-stream.ResultChan():
				if !ok {
					stream.Stop()
					if !sleepContext(ctx, c.PollInterval) {
						return Interrupt("watch interrupted")
					}
					goto reconnect
				}
				if event.Type == watch.Bookmark {
					continue
				}
				if event.Type == watch.Error {
					stream.Stop()
					if !sleepContext(ctx, c.PollInterval) {
						return Interrupt("watch interrupted")
					}
					goto reconnect
				}
				migration, ok := event.Object.(*virtv1.VirtualMachineInstanceMigration)
				if !ok {
					continue
				}
				info := kube.MigrationInfoFor(migration)
				uid := string(migration.UID)
				if event.Type == watch.Deleted && info.Active {
					info.Active = false
					info.State = "Deleted"
					info.Reason = "migration object deleted before a terminal phase was observed"
				}
				if info.Active {
					tracked[uid] = info
				}
				if _, ok := tracked[uid]; ok {
					c.emitWatchedMigration(info, nodes)
					if err := c.checkOutput(); err != nil {
						stream.Stop()
						return err
					}
					if !info.Active {
						delete(tracked, uid)
					}
				}
			}
		}
	reconnect:
	}
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(duration):
		return true
	}
}

func (c *Coordinator) emitWatchedMigration(migration kube.MigrationInfo, nodes map[string]bool) {
	source := migration.Source
	if source == "" {
		source = inferSource(migration.Migration.Annotations)
	}
	if len(nodes) > 0 && !nodes[source] {
		return
	}
	oldNode := c.Node
	c.Node = source
	c.emitMigration(migration, 0)
	c.Node = oldNode
}

func (c *Coordinator) Drain(parent context.Context, opts DrainOptions) (retErr error) {
	c.init()
	if err := validateDrainOptions(opts); err != nil {
		return err
	}
	started := c.Now()
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), opts.Timeout)
	defer cancel()
	if err := c.Clients.CheckDrainPermissions(runCtx); err != nil {
		return Operational("drain permissions: %v", err)
	}

	initial, err := c.Clients.Snapshot(runCtx, c.Node)
	if err != nil {
		return Operational("preflight: %v", err)
	}
	if err = preflight(initial, opts); err != nil {
		return err
	}
	if err = c.confirmMigrations(runCtx, initial); err != nil {
		return err
	}
	c.event("run", "ready", "preflight checks passed", ref("Node", "", c.Node, string(initial.Node.UID)), map[string]any{
		"vmiCount": len(initial.VMIs),
		"podCount": len(initial.Pods),
	})
	if err = c.checkOutput(); err != nil {
		return err
	}

	cutoff := c.Now()
	initialMigrationUIDs := map[string]bool{}
	for _, migration := range initial.Migrations {
		if migration.Active {
			initialMigrationUIDs[string(migration.Migration.UID)] = true
		}
	}
	if parent.Err() != nil {
		return Interrupt("drain interrupted before cordon")
	}
	cordonedByUs := !initial.Node.Spec.Unschedulable
	if cordonedByUs {
		if err = c.Clients.SetUnschedulable(runCtx, c.Node, true); err != nil {
			return Operational("cordon: %v", err)
		}
		if parent.Err() != nil {
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
			restoreErr := c.Clients.SetUnschedulable(restoreCtx, c.Node, false)
			restoreCancel()
			if restoreErr != nil {
				return Interrupt("drain interrupted during cordon; failed to restore scheduling: %v", restoreErr)
			}
			return Interrupt("drain interrupted during cordon; node scheduling was restored")
		}
		c.event("node", "cordoned", "node marked unschedulable", ref("Node", "", c.Node, string(initial.Node.UID)), nil)
		if err = c.checkOutput(); err != nil {
			return err
		}
	}

	snapshot, err := c.Clients.Snapshot(runCtx, c.Node)
	if err == nil {
		err = preflight(snapshot, opts)
	}
	if err == nil {
		err = c.confirmMigrations(runCtx, snapshot)
	}
	if err != nil {
		restoreMessage := ""
		if cordonedByUs {
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if restoreErr := c.Clients.SetUnschedulable(restoreCtx, c.Node, false); restoreErr != nil {
				restoreMessage = fmt.Sprintf("; failed to restore scheduling: %v", restoreErr)
			}
			restoreCancel()
		}
		return Operational("revalidate after cordon: %v%s", err, restoreMessage)
	}

	interrupted := false
	settledAbort := false
	defer func() {
		if retErr == nil || !interrupted || !settledAbort || !opts.AbortUncordons || !cordonedByUs {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if cleanupErr := c.Clients.SetUnschedulable(cleanupCtx, c.Node, false); cleanupErr == nil {
			c.event("node", "uncordoned", "interrupted drain settled and node was restored", ref("Node", "", c.Node, string(initial.Node.UID)), nil)
			if c.emitErr != nil {
				retErr = fmt.Errorf("%w; node was restored but reporting it failed: %v", retErr, c.emitErr)
			}
		} else {
			retErr = fmt.Errorf("%w; abort uncordon failed: %v", retErr, cleanupErr)
		}
	}()

	for _, pod := range snapshot.Pods {
		if parent.Err() != nil {
			interrupted = true
			break
		}
		if normalEligible(pod, opts) {
			if err = c.evictNormal(runCtx, pod, opts.GracePeriod); err != nil {
				return err
			}
			if err = c.checkOutput(); err != nil {
				return err
			}
		}
	}

	limit := c.Clients.OutboundMigrationLimit(runCtx)
	if opts.ParallelOutbound > 0 && opts.ParallelOutbound < limit {
		limit = opts.ParallelOutbound
	}
	triggered := map[string]bool{}
	failedSeen := map[string]map[string]bool{}
	metricsWarned := false
	initialVMIs := map[string][2]string{}
	targets := map[string]string{}
	durations := map[string]float64{}
	for _, vmi := range snapshot.VMIs {
		initialVMIs[vmi.VMI.Namespace+"/"+vmi.VMI.Name] = [2]string{vmi.VMI.Namespace, vmi.VMI.Name}
	}
	for _, migration := range snapshot.Migrations {
		if migration.Active {
			triggered[migration.Migration.Namespace+"/"+migration.Migration.Spec.VMIName] = true
		}
	}

	for {
		if err = c.checkOutput(); err != nil {
			return err
		}
		if parent.Err() != nil && !interrupted {
			interrupted = true
			c.event("run", "stopping", "interrupt received, waiting for active migrations", nil, nil)
		}
		if runCtx.Err() != nil {
			diagnostics := c.collectDiagnostics(snapshot)
			if interrupted {
				return Interrupt("drain interrupted while migrations were still active: %s", diagnostics)
			}
			return Timeout("drain timed out: %s", diagnostics)
		}

		snapshot, err = c.Clients.Snapshot(runCtx, c.Node)
		if err != nil {
			return Operational("observe drain: %v", err)
		}
		if !interrupted {
			err = preflight(snapshot, opts)
		}
		if err != nil {
			return Operational("safety recheck: %v; %s", err, c.collectDiagnostics(snapshot))
		}

		newFailures := observeFailed(failedSeen, snapshot.Migrations, cutoff, initialMigrationUIDs)
		for _, migration := range newFailures {
			key := migration.Migration.Namespace + "/" + migration.Migration.Spec.VMIName
			c.emitMigration(migration, len(failedSeen[key]))
		}
		for _, migration := range snapshot.Migrations {
			key := migration.Migration.Namespace + "/" + migration.Migration.Spec.VMIName
			if migration.Target != "" {
				targets[key] = migration.Target
			}
			if elapsed, ok := migrationDuration(migration); ok {
				durations[key] = elapsed.Seconds()
			}
			c.emitMigration(migration, len(failedSeen[key]))
		}
		for vmiKey, failures := range failedSeen {
			if len(failures) > opts.Retries {
				return Operational("VMI %s exceeded failed migration retry budget (%d); KubeVirt may continue its evacuation retry; %s", vmiKey, opts.Retries, c.collectDiagnostics(snapshot))
			}
		}
		c.emitTransferMetrics(runCtx, snapshot, &metricsWarned)
		if err = c.checkOutput(); err != nil {
			return err
		}

		if !interrupted {
			for _, pod := range snapshot.Pods {
				if parent.Err() != nil {
					interrupted = true
					break
				}
				if normalEligible(pod, opts) {
					if err = c.evictNormal(runCtx, pod, opts.GracePeriod); err != nil {
						return err
					}
				}
			}
			if !interrupted {
				outstanding, activeByVMI := outstandingMigrations(snapshot, triggered)
				for _, vmi := range snapshot.VMIs {
					if parent.Err() != nil {
						interrupted = true
						break
					}
					key := vmi.VMI.Namespace + "/" + vmi.VMI.Name
					if outstanding >= limit || triggered[key] || activeByVMI[key] || vmi.Launcher == nil {
						continue
					}
					if err = c.triggerVMI(runCtx, vmi); err != nil {
						return err
					}
					triggered[key] = true
					outstanding++
				}
				if !interrupted {
					err = c.reEvictStaleLaunchers(runCtx, snapshot, triggered)
				}
				if err != nil {
					return err
				}
			}
		}
		active := countActive(snapshot.Migrations)
		if interrupted && active == 0 {
			if len(snapshot.VMIs) == 0 {
				settledAbort = true
				return Interrupt("drain interrupted after active migrations settled: %s", diagnosticSummary(snapshot))
			}
			return Interrupt("drain interrupted with no active migration and %d VMI(s) still on the source; node remains cordoned: %s", len(snapshot.VMIs), diagnosticSummary(snapshot))
		}
		if len(snapshot.VMIs) == 0 && active == 0 && !hasEligiblePods(snapshot, opts) {
			ready, verifyErr := c.verifyHotplug(runCtx, initialVMIs)
			if verifyErr != nil {
				return verifyErr
			}
			if ready {
				c.event("summary", "succeeded", "node drain completed", ref("Node", "", c.Node, ""), map[string]any{
					"elapsedSeconds":   c.Now().Sub(started).Seconds(),
					"vmiCount":         len(initialVMIs),
					"failedAttempts":   failureCount(failedSeen),
					"hotplugVerified":  true,
					"targets":          targets,
					"durationsSeconds": durations,
				})
				c.event("node", "drained", "no source workloads remain", ref("Node", "", c.Node, ""), nil)
				if err = c.checkOutput(); err != nil {
					return err
				}
				return nil
			}
		}
		if interrupted {
			select {
			case <-runCtx.Done():
			case <-time.After(c.PollInterval):
			}
		} else {
			select {
			case <-runCtx.Done():
			case <-parent.Done():
				interrupted = true
			case <-time.After(c.PollInterval):
			}
		}
	}
}

func migrationDuration(migration kube.MigrationInfo) (time.Duration, bool) {
	state := migration.Migration.Status.MigrationState
	if state == nil || state.StartTimestamp == nil || state.EndTimestamp == nil {
		return 0, false
	}
	return state.EndTimestamp.Sub(state.StartTimestamp.Time), true
}

func validateDrainOptions(opts DrainOptions) error {
	if opts.Timeout <= 0 {
		return Usage("timeout must be positive")
	}
	if opts.Retries < 0 {
		return Usage("retries must not be negative")
	}
	if opts.ParallelOutbound < 0 {
		return Usage("parallel-outbound must not be negative")
	}
	return nil
}

func (c *Coordinator) confirmMigrations(ctx context.Context, snapshot *kube.Snapshot) error {
	var blockers []string
	active := map[string]bool{}
	for _, migration := range snapshot.Migrations {
		if migration.Active {
			active[migration.Migration.Namespace+"/"+migration.Migration.Spec.VMIName] = true
		}
	}
	for _, vmi := range snapshot.VMIs {
		if vmi.Blocker != "" {
			continue
		}
		if active[vmi.VMI.Namespace+"/"+vmi.VMI.Name] {
			continue
		}
		if err := c.Clients.ConfirmEvacuation(ctx, vmi); err != nil {
			blockers = append(blockers, fmt.Sprintf("VMI %s/%s: %v", vmi.VMI.Namespace, vmi.VMI.Name, err))
		}
	}
	if len(blockers) > 0 {
		return Operational("live migration confirmation failed:\n  %s", strings.Join(blockers, "\n  "))
	}
	return nil
}

func (c *Coordinator) emitMigration(migration kube.MigrationInfo, retry int) {
	migrationState := strings.ToLower(valueOr(migration.State, "pending"))
	c.event("migration", migrationState, migration.Reason, ref("VirtualMachineInstanceMigration", migration.Migration.Namespace, migration.Migration.Name, string(migration.Migration.UID)), map[string]any{
		"source": migration.Source,
		"target": migration.Target,
		"vmi":    migration.Migration.Spec.VMIName,
		"retry":  retry,
	})
}

func (c *Coordinator) emitTransferMetrics(ctx context.Context, snapshot *kube.Snapshot, warned *bool) {
	transfers, err := c.Clients.SourceMetrics(ctx, c.Node)
	if err != nil {
		if !*warned {
			c.event("xfer", "unavailable", "transfer metrics unavailable", nil, map[string]any{"reason": err.Error()})
			*warned = true
		}
		return
	}
	for _, vmi := range snapshot.VMIs {
		key := vmi.VMI.Namespace + "/" + vmi.VMI.Name
		transfer, ok := transfers[key]
		if !ok {
			continue
		}
		details := map[string]any{"processedBytes": transfer.Processed, "remainingBytes": transfer.Remaining, "diskRateBytes": transfer.DiskRate, "memoryRateBytes": transfer.MemoryRate}
		if transfer.Processed != nil && transfer.Remaining != nil {
			details["totalBytes"] = *transfer.Processed + *transfer.Remaining
		}
		c.event("xfer", "observed", "migration transfer updated", ref("VirtualMachineInstance", vmi.VMI.Namespace, vmi.VMI.Name, string(vmi.VMI.UID)), details)
	}
}

func (c *Coordinator) triggerVMI(ctx context.Context, vmi kube.VMIInfo) error {
	if vmi.Blocker != "" {
		return Operational("VMI %s/%s became unsafe to evict: %s", vmi.VMI.Namespace, vmi.VMI.Name, vmi.Blocker)
	}
	if err := c.Clients.ConfirmEvacuation(ctx, vmi); err != nil {
		return Operational("confirm migration for %s/%s immediately before eviction: %v", vmi.VMI.Namespace, vmi.VMI.Name, err)
	}
	err := c.Clients.EvictPod(ctx, vmi.Launcher, -1, false)
	if err != nil && !kube.IsKubeVirtEvacuationAccepted(err, vmi.VMI.Namespace, vmi.VMI.Name) {
		return Operational("trigger migration for %s/%s: %v", vmi.VMI.Namespace, vmi.VMI.Name, err)
	}
	c.event("vmi", "triggered", "KubeVirt evacuation requested", ref("VirtualMachineInstance", vmi.VMI.Namespace, vmi.VMI.Name, string(vmi.VMI.UID)), nil)
	return c.checkOutput()
}

func (c *Coordinator) evictNormal(ctx context.Context, pod kube.PodInfo, grace int64) error {
	err := c.Clients.EvictPod(ctx, pod.Pod, grace, false)
	if err == nil || apierrors.IsNotFound(err) {
		c.event("pod", "evicting", "eviction accepted", ref("Pod", pod.Pod.Namespace, pod.Pod.Name, string(pod.Pod.UID)), nil)
		return c.checkOutput()
	}
	if kube.IsRetryablePDB(err) {
		pdbs := kube.BlockingPDBs(pod, true)
		message := "pod disruption budget blocks eviction"
		if len(pdbs) > 0 {
			message = pdbMessage(pdbs)
		}
		c.event("pod", "blocked", message, ref("Pod", pod.Pod.Namespace, pod.Pod.Name, string(pod.Pod.UID)), map[string]any{"pdbs": pdbs})
		return c.checkOutput()
	}
	return Operational("evict pod %s/%s: %v", pod.Pod.Namespace, pod.Pod.Name, err)
}

func (c *Coordinator) reEvictStaleLaunchers(ctx context.Context, snapshot *kube.Snapshot, triggered map[string]bool) error {
	current := map[string]bool{}
	for _, vmi := range snapshot.VMIs {
		if vmi.Launcher != nil {
			current[vmi.Launcher.Namespace+"/"+vmi.Launcher.Name] = true
		}
	}
	for _, pod := range snapshot.Pods {
		key := pod.Pod.Namespace + "/" + pod.Pod.Name
		if !pod.Launcher || current[key] || triggered["stale:"+key] {
			continue
		}
		err := c.Clients.EvictPod(ctx, pod.Pod, -1, false)
		if err != nil && !apierrors.IsNotFound(err) && !kube.IsRetryablePDB(err) {
			return Operational("re-evict source launcher %s: %v", key, err)
		}
		if kube.IsRetryablePDB(err) {
			pdbs := kube.BlockingPDBs(pod, false)
			if len(pdbs) > 0 {
				c.event("vmi", "blocked", pdbMessage(pdbs), ref("Pod", pod.Pod.Namespace, pod.Pod.Name, string(pod.Pod.UID)), map[string]any{"pdbs": pdbs})
			}
			continue
		}
		triggered["stale:"+key] = true
		c.event("pod", "evicting", "source launcher cleanup accepted", ref("Pod", pod.Pod.Namespace, pod.Pod.Name, string(pod.Pod.UID)), nil)
	}
	return nil
}

func (c *Coordinator) verifyHotplug(ctx context.Context, initial map[string][2]string) (bool, error) {
	allReady := true
	for _, item := range initial {
		expected, ready, detail, err := c.Clients.HotplugReady(ctx, item[0], item[1], "")
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return false, Operational("verify hotplug for %s/%s: %v", item[0], item[1], err)
		}
		if ready < expected {
			allReady = false
			c.event("hotplug", "pending", detail, ref("VirtualMachineInstance", item[0], item[1], ""), map[string]any{"expected": expected, "ready": ready})
		} else if expected > 0 {
			c.event("hotplug", "ready", "all hotplug volumes are ready on the target", ref("VirtualMachineInstance", item[0], item[1], ""), map[string]any{"expected": expected, "ready": ready})
		}
	}
	return allReady, nil
}

func preflight(snapshot *kube.Snapshot, opts DrainOptions) error {
	var blockers []string
	for _, vmi := range snapshot.VMIs {
		if vmi.Blocker != "" {
			blockers = append(blockers, fmt.Sprintf("VMI %s/%s: %s", vmi.VMI.Namespace, vmi.VMI.Name, vmi.Blocker))
		}
	}
	for _, pod := range snapshot.Pods {
		if pod.Launcher || pod.Hotplug || pod.Ignored {
			continue
		}
		if !pod.Managed && !opts.Force {
			blockers = append(blockers, fmt.Sprintf("pod %s/%s is unmanaged (use --force)", pod.Pod.Namespace, pod.Pod.Name))
		}
		if pod.EmptyDir && !opts.DeleteEmptyDirData {
			blockers = append(blockers, fmt.Sprintf("pod %s/%s uses emptyDir (use --delete-emptydir-data)", pod.Pod.Namespace, pod.Pod.Name))
		}
	}
	if len(blockers) > 0 {
		return Operational("preflight blocked:\n  %s", strings.Join(blockers, "\n  "))
	}
	return nil
}

func normalEligible(pod kube.PodInfo, opts DrainOptions) bool {
	return !pod.Launcher && !pod.Hotplug && !pod.Ignored && (pod.Managed || opts.Force) && (!pod.EmptyDir || opts.DeleteEmptyDirData)
}

func hasEligiblePods(snapshot *kube.Snapshot, opts DrainOptions) bool {
	for _, pod := range snapshot.Pods {
		if normalEligible(pod, opts) || pod.Launcher || pod.Hotplug {
			return true
		}
	}
	return false
}

func outstandingMigrations(snapshot *kube.Snapshot, triggered map[string]bool) (int, map[string]bool) {
	activeByVMI := map[string]bool{}
	outstanding := 0
	for _, migration := range snapshot.Migrations {
		if !migration.Active {
			continue
		}
		key := migration.Migration.Namespace + "/" + migration.Migration.Spec.VMIName
		activeByVMI[key] = true
		outstanding++
	}
	for _, vmi := range snapshot.VMIs {
		key := vmi.VMI.Namespace + "/" + vmi.VMI.Name
		if triggered[key] && !activeByVMI[key] {
			outstanding++
		}
	}
	return outstanding, activeByVMI
}

func countActive(migrations []kube.MigrationInfo) int {
	count := 0
	for _, migration := range migrations {
		if migration.Active {
			count++
		}
	}
	return count
}

func observeFailed(seen map[string]map[string]bool, migrations []kube.MigrationInfo, cutoff time.Time, initialUIDs map[string]bool) []kube.MigrationInfo {
	var newlyObserved []kube.MigrationInfo
	for _, migration := range migrations {
		if !migration.Failed {
			continue
		}
		uid := string(migration.Migration.UID)
		if initialUIDs[uid] || migration.Migration.CreationTimestamp.Time.Before(cutoff) {
			continue
		}
		key := migration.Migration.Namespace + "/" + migration.Migration.Spec.VMIName
		if seen[key] == nil {
			seen[key] = map[string]bool{}
		}
		if seen[key][uid] {
			continue
		}
		seen[key][uid] = true
		newlyObserved = append(newlyObserved, migration)
	}
	return newlyObserved
}

func failureCount(seen map[string]map[string]bool) int {
	count := 0
	for _, failures := range seen {
		count += len(failures)
	}
	return count
}

func diagnosticSummary(snapshot *kube.Snapshot) string {
	if snapshot == nil {
		return "state unavailable"
	}
	var details []string
	for _, migration := range snapshot.Migrations {
		if migration.Active || migration.Failed {
			details = append(details, fmt.Sprintf("VMIM %s/%s=%s (%s)", migration.Migration.Namespace, migration.Migration.Name, valueOr(migration.State, "Pending"), valueOr(migration.Reason, "no message")))
		}
	}
	for _, pod := range snapshot.Pods {
		if pdbs := kube.BlockingPDBs(pod, !pod.Launcher); len(pdbs) > 0 {
			details = append(details, fmt.Sprintf("pod %s/%s: %s", pod.Pod.Namespace, pod.Pod.Name, pdbMessage(pdbs)))
		}
		for _, condition := range pod.Pod.Status.Conditions {
			if condition.Type == "PodScheduled" && condition.Status == "False" && condition.Message != "" {
				details = append(details, fmt.Sprintf("pod %s/%s: %s", pod.Pod.Namespace, pod.Pod.Name, condition.Message))
			}
		}
	}
	sort.Strings(details)
	if len(details) == 0 {
		return "no detailed blocker reported"
	}
	return strings.Join(details, "; ")
}

func (c *Coordinator) collectDiagnostics(snapshot *kube.Snapshot) string {
	summary := diagnosticSummary(snapshot)
	if snapshot == nil {
		return summary
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var extra []string
	for i := range snapshot.TargetPods {
		pod := &snapshot.TargetPods[i]
		for _, condition := range pod.Status.Conditions {
			if condition.Status == "False" && condition.Message != "" {
				message := fmt.Sprintf("target pod %s/%s %s: %s", pod.Namespace, pod.Name, condition.Type, condition.Message)
				extra = append(extra, message)
				c.event("diagnostic", "blocked", message, ref("Pod", pod.Namespace, pod.Name, string(pod.UID)), nil)
			}
		}
		events, err := c.Clients.WarningEventsForPod(ctx, pod)
		if err != nil {
			continue
		}
		for _, event := range events {
			message := fmt.Sprintf("target pod %s/%s: %s", pod.Namespace, pod.Name, event)
			extra = append(extra, message)
			c.event("diagnostic", "warning", message, ref("Pod", pod.Namespace, pod.Name, string(pod.UID)), nil)
		}
	}
	if len(extra) == 0 {
		return summary
	}
	sort.Strings(extra)
	return summary + "; " + strings.Join(extra, "; ")
}

func pdbMessage(pdbs []kube.PDBInfo) string {
	parts := make([]string, 0, len(pdbs))
	for _, pdb := range pdbs {
		parts = append(parts, fmt.Sprintf("PDB %s allows %d disruptions (%d/%d healthy)", pdb.Name, pdb.DisruptionsAllowed, pdb.CurrentHealthy, pdb.DesiredHealthy))
	}
	return strings.Join(parts, ", ")
}

func inferSource(annotations map[string]string) string {
	for key, value := range annotations {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "evacuation") || strings.Contains(lower, "source-node") {
			return value
		}
	}
	return ""
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
