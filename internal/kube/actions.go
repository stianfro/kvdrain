package kube

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	virtv1 "kubevirt.io/api/core/v1"
)

const (
	FieldManager          = "kvdrain"
	CordonOwnerAnnotation = "kvdrain.io/cordon-owner"
	LeaseNamespace        = "kube-system"
)

func (c Clients) SetUnschedulable(ctx context.Context, name string, value bool) error {
	body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{CordonOwnerAnnotation: nil}}, "spec": map[string]any{"unschedulable": value}})
	_, err := c.Core.CoreV1().Nodes().Patch(ctx, name, types.MergePatchType, body, metav1.PatchOptions{FieldManager: FieldManager})
	if err != nil {
		return fmt.Errorf("set node %q unschedulable=%t: %w", name, value, err)
	}
	return nil
}

// Cordon marks a node unschedulable and records run-specific ownership for a
// conditional rollback. The returned resource version must be used verbatim.
func (c Clients) Cordon(ctx context.Context, name, runID string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": name, "annotations": map[string]string{CordonOwnerAnnotation: runID}},
		"spec":     map[string]any{"unschedulable": true},
	})
	force := false
	node, err := c.Core.CoreV1().Nodes().Patch(ctx, name, types.ApplyPatchType, body, metav1.PatchOptions{FieldManager: fieldManager(runID), Force: &force})
	if err != nil {
		return "", fmt.Errorf("cordon node %q: %w", name, err)
	}
	return node.ResourceVersion, nil
}

// RollbackCordon only uncordons the exact node version still owned by this run.
// Any concurrent change makes the patch fail closed and leaves the node cordoned.
func (c Clients) RollbackCordon(ctx context.Context, name, runID string, nodeUID types.UID) error {
	node, err := c.Core.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read node %q before conditional restore: %w", name, err)
	}
	body, _ := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": nodeUID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": node.ResourceVersion},
		{"op": "test", "path": "/metadata/annotations/kvdrain.io~1cordon-owner", "value": runID},
		{"op": "test", "path": "/spec/unschedulable", "value": true},
		{"op": "replace", "path": "/spec/unschedulable", "value": false},
		{"op": "remove", "path": "/metadata/annotations/kvdrain.io~1cordon-owner"},
	})
	if _, err = c.Core.CoreV1().Nodes().Patch(ctx, name, types.JSONPatchType, body, metav1.PatchOptions{FieldManager: fieldManager(runID)}); err != nil {
		return fmt.Errorf("conditionally restore node %q: %w", name, err)
	}
	return nil
}

func fieldManager(runID string) string {
	if runID == "" {
		return FieldManager
	}
	return FieldManager + "-" + runID
}

func leaseName(node string) string {
	sum := sha256.Sum256([]byte(node))
	return fmt.Sprintf("kvdrain-%x", sum[:12])
}

func (c Clients) AcquireDrainLease(ctx context.Context, node, holder string, duration time.Duration) error {
	seconds := int64(duration/time.Second) + 60
	if seconds < 60 {
		seconds = 60
	}
	if seconds > math.MaxInt32 {
		seconds = math.MaxInt32
	}
	now := metav1.NewMicroTime(time.Now().UTC())
	value := int32(seconds)
	name := leaseName(node)
	lease := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: LeaseNamespace}, Spec: coordinationv1.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &value, AcquireTime: &now, RenewTime: &now}}
	if _, err := c.Core.CoordinationV1().Leases(LeaseNamespace).Create(ctx, lease, metav1.CreateOptions{}); err == nil {
		return nil
	} else if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("acquire node drain lease: %w", err)
	}
	existing, err := c.Core.CoordinationV1().Leases(LeaseNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read node drain lease: %w", err)
	}
	if existing.Spec.HolderIdentity != nil && *existing.Spec.HolderIdentity == holder {
		return nil
	}
	owner := "unknown"
	if existing.Spec.HolderIdentity != nil {
		owner = *existing.Spec.HolderIdentity
	}
	return fmt.Errorf("node %q is already being drained by %s; remove stale lease %s/%s only after verifying no drain is active", node, owner, LeaseNamespace, name)
}

func (c Clients) ReleaseDrainLease(ctx context.Context, node, holder string) error {
	leases := c.Core.CoordinationV1().Leases(LeaseNamespace)
	existing, err := leases.Get(ctx, leaseName(node), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.Spec.HolderIdentity == nil || *existing.Spec.HolderIdentity != holder {
		return fmt.Errorf("node drain lease ownership changed")
	}
	uid, rv := existing.UID, existing.ResourceVersion
	return leases.Delete(ctx, existing.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
}

func (c Clients) EvictPod(ctx context.Context, pod *corev1.Pod, grace int64, dryRun bool) error {
	uid := pod.UID
	options := &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}
	if grace >= 0 {
		options.GracePeriodSeconds = &grace
	}
	ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}, DeleteOptions: options}
	if dryRun {
		ev.DeleteOptions.DryRun = []string{metav1.DryRunAll}
	}
	return c.Core.PolicyV1().Evictions(pod.Namespace).Evict(ctx, ev)
}

func IsRetryablePDB(err error) bool {
	return apierrors.IsTooManyRequests(err) && strings.Contains(strings.ToLower(err.Error()), "disruption budget")
}

func IsKubeVirtEvacuationAccepted(err error, namespace, vmiName string) bool {
	if !apierrors.IsTooManyRequests(err) {
		return false
	}
	expected := fmt.Sprintf("Eviction triggered evacuation of VMI \"%s/%s\"", namespace, vmiName)
	return strings.Contains(err.Error(), expected)
}

func (c Clients) ConfirmEvacuation(ctx context.Context, vmi VMIInfo) error {
	if vmi.Launcher == nil {
		return fmt.Errorf("launcher pod not found")
	}
	verifiedPDB := false
	for _, pdb := range vmi.LauncherPDBs {
		if pdb.KubeVirtOwned && pdb.DisruptionsAllowed == 0 {
			verifiedPDB = true
			break
		}
	}
	if !verifiedPDB {
		return fmt.Errorf("verified KubeVirt-owned blocking PDB not found for launcher")
	}
	err := c.EvictPod(ctx, vmi.Launcher, -1, true)
	if IsKubeVirtEvacuationAccepted(err, vmi.VMI.Namespace, vmi.VMI.Name) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("dry-run eviction would remove the launcher without triggering live migration")
	}
	return fmt.Errorf("dry-run eviction did not confirm live migration: %w", err)
}

func BlockingPDBs(pod PodInfo, includeKubeVirt bool) []PDBInfo {
	var out []PDBInfo
	for _, pdb := range pod.PDBs {
		if pdb.DisruptionsAllowed == 0 && (includeKubeVirt || !pdb.KubeVirtOwned) {
			out = append(out, pdb)
		}
	}
	return out
}

func (c Clients) WarningEventsForPod(ctx context.Context, pod *corev1.Pod) ([]string, error) {
	selector := fmt.Sprintf("involvedObject.uid=%s,type=Warning", pod.UID)
	events, err := c.Core.CoreV1().Events(pod.Namespace).List(ctx, metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		return nil, err
	}
	messages := make([]string, 0, len(events.Items))
	for _, event := range events.Items {
		messages = append(messages, fmt.Sprintf("%s: %s", event.Reason, event.Message))
	}
	return messages, nil
}

func (c Clients) ListMigrations(ctx context.Context) ([]MigrationInfo, string, error) {
	list, err := c.Virt.VirtualMachineInstanceMigration(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("list virtual machine instance migrations: %w", err)
	}
	out := make([]MigrationInfo, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, MigrationInfoFor(&list.Items[i]))
	}
	return out, list.ResourceVersion, nil
}

func (c Clients) WatchMigrations(ctx context.Context, resourceVersion string) (watch.Interface, error) {
	return c.Virt.VirtualMachineInstanceMigration(corev1.NamespaceAll).Watch(ctx, metav1.ListOptions{ResourceVersion: resourceVersion, AllowWatchBookmarks: true})
}

func MigrationInfoFor(m *virtv1.VirtualMachineInstanceMigration) MigrationInfo {
	source, target := "", ""
	if m.Status.MigrationState != nil {
		source = m.Status.MigrationState.SourceNode
		target = m.Status.MigrationState.TargetNode
	}
	if source == "" {
		source = inferMigrationSource(m.Annotations)
	}
	state := string(m.Status.Phase)
	return MigrationInfo{Migration: m, Source: source, Target: target, State: state, Reason: migrationReason(m), Active: m.Status.Phase != virtv1.MigrationSucceeded && m.Status.Phase != virtv1.MigrationFailed, Failed: state == string(virtv1.MigrationFailed)}
}

func (c Clients) HotplugReady(ctx context.Context, namespace, name string, uid types.UID, expectedNames []string, target string) (expected, ready int, detail string, err error) {
	vmi, e := c.Virt.VirtualMachineInstance(namespace).Get(ctx, name, metav1.GetOptions{})
	if e != nil {
		return 0, 0, "", e
	}
	if vmi.UID != uid {
		return 0, 0, "", fmt.Errorf("VMI UID changed from %s to %s", uid, vmi.UID)
	}
	pods, e := c.Core.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if e != nil {
		return 0, 0, "", e
	}
	expected, ready = hotplugStateForNames(vmi, pods.Items, target, expectedNames)
	if ready < expected {
		attachmentUIDs := map[types.UID]bool{}
		attachmentNames := map[string]bool{}
		for _, volume := range vmi.Status.VolumeStatus {
			if volume.HotplugVolume != nil {
				attachmentUIDs[volume.HotplugVolume.AttachPodUID] = true
				attachmentNames[volume.HotplugVolume.AttachPodName] = true
			}
		}
		if vmi.Status.MigrationState != nil {
			attachmentUIDs[vmi.Status.MigrationState.TargetAttachmentPodUID] = true
		}
		for i := range pods.Items {
			p := &pods.Items[i]
			if (!attachmentUIDs[p.UID] && !attachmentNames[p.Name]) || p.Status.Phase == corev1.PodRunning {
				continue
			}
			for _, condition := range p.Status.Conditions {
				if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
					detail = condition.Message
					break
				}
			}
		}
	}
	return expected, ready, detail, nil
}

// OutboundMigrationLimit discovers the cluster setting without assuming an install namespace.
func (c Clients) OutboundMigrationLimit(ctx context.Context) int {
	limit := 2
	list, err := c.Virt.KubeVirt(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return limit
	}
	for i := range list.Items {
		cfg := list.Items[i].Spec.Configuration.MigrationConfiguration
		if cfg != nil && cfg.ParallelOutboundMigrationsPerNode != nil && *cfg.ParallelOutboundMigrationsPerNode > 0 {
			return int(*cfg.ParallelOutboundMigrationsPerNode)
		}
	}
	return limit
}
