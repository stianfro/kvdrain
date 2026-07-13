package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

type Clients struct {
	Core kubernetes.Interface
	Virt kubecli.KubevirtClient
}

type Snapshot struct {
	Node       *corev1.Node
	Nodes      []corev1.Node
	VMIs       []VMIInfo
	Migrations []MigrationInfo
	Pods       []PodInfo
	TargetPods []corev1.Pod
}

type VMIInfo struct {
	VMI              *virtv1.VirtualMachineInstance
	Migratable       bool
	MigratableReason string
	EvictionStrategy string
	EligibleTargets  []string
	HotplugExpected  int
	HotplugReady     int
	HotplugVolumes   []string
	Launcher         *corev1.Pod
	LauncherPDBs     []PDBInfo
	Blocker          string
}

type MigrationInfo struct {
	Migration *virtv1.VirtualMachineInstanceMigration
	Source    string
	Target    string
	State     string
	Reason    string
	Active    bool
	Failed    bool
}

type PodInfo struct {
	Pod      *corev1.Pod
	Launcher bool
	// UnverifiedLauncher marks launcher-like metadata that could not be bound
	// to the VMI controller's ActivePods status.
	UnverifiedLauncher bool
	Hotplug            bool
	Ignored            bool
	Managed            bool
	EmptyDir           bool
	Blocker            string
	PDBs               []PDBInfo
}

type PDBInfo struct {
	Name               string `json:"name"`
	DisruptionsAllowed int32  `json:"disruptionsAllowed"`
	CurrentHealthy     int32  `json:"currentHealthy"`
	DesiredHealthy     int32  `json:"desiredHealthy"`
	KubeVirtOwned      bool   `json:"kubeVirtOwned"`
}

func (c Clients) Snapshot(ctx context.Context, nodeName string) (*Snapshot, error) {
	node, err := c.Core.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get node %q: %w", nodeName, err)
	}
	nodes, err := c.Core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	pods, err := c.Core.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	pvs, err := c.Core.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list persistent volumes: %w", err)
	}
	pvcs, err := c.Core.CoreV1().PersistentVolumeClaims(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list persistent volume claims: %w", err)
	}
	pdbs, err := c.Core.PolicyV1().PodDisruptionBudgets(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pod disruption budgets: %w", err)
	}
	vmis, err := c.Virt.VirtualMachineInstance(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list virtual machine instances: %w", err)
	}
	migrations, err := c.Virt.VirtualMachineInstanceMigration(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list virtual machine instance migrations: %w", err)
	}

	s := &Snapshot{Node: node, Nodes: nodes.Items}
	vmiByUID := map[types.UID]*virtv1.VirtualMachineInstance{}
	hotplugUIDs := map[types.UID]bool{}
	hotplugNames := map[string]bool{}
	for i := range vmis.Items {
		vmiByUID[vmis.Items[i].UID] = &vmis.Items[i]
		for _, volume := range vmis.Items[i].Status.VolumeStatus {
			if volume.HotplugVolume != nil {
				hotplugUIDs[volume.HotplugVolume.AttachPodUID] = true
				hotplugNames[volume.HotplugVolume.AttachPodName] = true
			}
		}
		if vmis.Items[i].Status.MigrationState != nil {
			hotplugUIDs[vmis.Items[i].Status.MigrationState.TargetAttachmentPodUID] = true
		}
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName != nodeName {
			continue
		}
		info := classifyPod(p, hotplugUIDs, hotplugNames, vmiByUID)
		info.PDBs = matchingPDBs(p, pdbs.Items, vmiByUID)
		s.Pods = append(s.Pods, info)
	}
	for i := range vmis.Items {
		vmi := &vmis.Items[i]
		if vmi.Status.NodeName != nodeName {
			continue
		}
		info := VMIInfo{VMI: vmi}
		info.Migratable, info.MigratableReason = migratability(vmi)
		info.EvictionStrategy = stringValue(vmi.Spec.EvictionStrategy)
		if info.EvictionStrategy == "" {
			info.EvictionStrategy = c.vmEvictionStrategy(ctx, vmi)
		}
		info.Launcher = launcherFor(vmi, pods.Items)
		if info.Launcher != nil {
			info.LauncherPDBs = matchingPDBs(info.Launcher, pdbs.Items, vmiByUID)
		}
		info.EligibleTargets = eligibleTargets(vmi, info.Launcher, nodes.Items, pvcs.Items, pvs.Items, nodeName)
		info.HotplugVolumes, err = c.expectedHotplugVolumeNames(ctx, vmi)
		if err != nil {
			return nil, fmt.Errorf("derive hotplug volumes for VMI %s/%s: %w", vmi.Namespace, vmi.Name, err)
		}
		info.HotplugExpected, info.HotplugReady = hotplugStateForNames(vmi, pods.Items, "", info.HotplugVolumes)
		if !info.Migratable {
			info.Blocker = "VMI is not LiveMigratable: " + info.MigratableReason
		} else if info.EvictionStrategy != string(virtv1.EvictionStrategyLiveMigrate) && info.EvictionStrategy != string(virtv1.EvictionStrategyLiveMigrateIfPossible) {
			info.Blocker = "effective eviction strategy is " + valueOr(info.EvictionStrategy, "unset")
		} else if len(info.EligibleTargets) == 0 {
			info.Blocker = "no eligible target node"
		}
		s.VMIs = append(s.VMIs, info)
	}
	for i := range migrations.Items {
		m := &migrations.Items[i]
		source := ""
		target := ""
		if m.Status.MigrationState != nil {
			source = m.Status.MigrationState.SourceNode
			target = m.Status.MigrationState.TargetNode
		}
		if source == "" {
			source = inferMigrationSource(m.Annotations)
		}
		if source != nodeName {
			continue
		}
		state := string(m.Status.Phase)
		mi := MigrationInfo{Migration: m, Source: source, Target: target, State: state, Reason: migrationReason(m)}
		mi.Active = m.Status.Phase != virtv1.MigrationSucceeded && m.Status.Phase != virtv1.MigrationFailed
		mi.Failed = state == string(virtv1.MigrationFailed)
		s.Migrations = append(s.Migrations, mi)
	}
	targetNames := map[string]bool{}
	targetUIDs := map[types.UID]bool{}
	for _, migration := range s.Migrations {
		if migration.Migration.Status.MigrationState != nil {
			targetNames[migration.Migration.Namespace+"/"+migration.Migration.Status.MigrationState.TargetPod] = true
			targetUIDs[migration.Migration.Status.MigrationState.TargetAttachmentPodUID] = true
		}
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if targetNames[pod.Namespace+"/"+pod.Name] || targetUIDs[pod.UID] {
			s.TargetPods = append(s.TargetPods, *pod.DeepCopy())
		}
	}
	sort.Slice(s.VMIs, func(i, j int) bool {
		return key(s.VMIs[i].VMI.Namespace, s.VMIs[i].VMI.Name) < key(s.VMIs[j].VMI.Namespace, s.VMIs[j].VMI.Name)
	})
	return s, nil
}

func ClassifyPod(p *corev1.Pod) PodInfo { return classifyPod(p, nil, nil, nil) }
func classifyPod(p *corev1.Pod, hotplugUIDs map[types.UID]bool, hotplugNames map[string]bool, vmis map[types.UID]*virtv1.VirtualMachineInstance) PodInfo {
	out := PodInfo{Pod: p}
	out.Launcher = verifiedLauncherVMI(p, vmis) != nil
	out.Hotplug = hotplugUIDs[p.UID] || hotplugNames[p.Name]
	if out.Launcher || out.Hotplug {
		return out
	}
	out.UnverifiedLauncher = looksLikeLauncher(p)
	if p.Annotations[corev1.MirrorPodAnnotationKey] != "" || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed || controlledByKind(p, "DaemonSet") {
		out.Ignored = true
		return out
	}
	out.Managed = hasController(p) && !out.UnverifiedLauncher
	for _, v := range p.Spec.Volumes {
		if v.EmptyDir != nil {
			out.EmptyDir = true
			break
		}
	}
	if out.UnverifiedLauncher {
		out.Blocker = "launcher-like pod is not present in verified VMI ActivePods status"
	} else if !out.Managed {
		out.Blocker = "unmanaged pod"
	} else if out.EmptyDir {
		out.Blocker = "pod uses emptyDir"
	}
	return out
}

func migratability(vmi *virtv1.VirtualMachineInstance) (bool, string) {
	for _, c := range vmi.Status.Conditions {
		if c.Type == virtv1.VirtualMachineInstanceIsMigratable {
			if c.Status == corev1.ConditionTrue {
				return true, c.Message
			}
			if c.Message != "" {
				return false, c.Message
			}
			return false, c.Reason
		}
	}
	return false, "LiveMigratable condition is missing"
}

func (c Clients) vmEvictionStrategy(ctx context.Context, vmi *virtv1.VirtualMachineInstance) string {
	for _, owner := range vmi.OwnerReferences {
		if owner.Kind != "VirtualMachine" {
			continue
		}
		vm, err := c.Virt.VirtualMachine(vmi.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err != nil {
			return ""
		}
		if vm.Spec.Template != nil {
			if strategy := stringValue(vm.Spec.Template.Spec.EvictionStrategy); strategy != "" {
				return strategy
			}
		}
	}
	list, err := c.Virt.KubeVirt(corev1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err == nil {
		for i := range list.Items {
			if strategy := stringValue(list.Items[i].Spec.Configuration.EvictionStrategy); strategy != "" {
				return strategy
			}
		}
	}
	return ""
}

func eligibleTargets(vmi *virtv1.VirtualMachineInstance, launcher *corev1.Pod, nodes []corev1.Node, pvcs []corev1.PersistentVolumeClaim, pvs []corev1.PersistentVolume, source string) []string {
	pvByName := map[string]*corev1.PersistentVolume{}
	for i := range pvs {
		pvByName[pvs[i].Name] = &pvs[i]
	}
	pvcByName := map[string]*corev1.PersistentVolumeClaim{}
	for i := range pvcs {
		if pvcs[i].Namespace == vmi.Namespace {
			pvcByName[pvcs[i].Name] = &pvcs[i]
		}
	}
	var out []string
	selector := map[string]string{}
	for key, value := range vmi.Spec.NodeSelector {
		selector[key] = value
	}
	affinity := vmi.Spec.Affinity
	tolerations := vmi.Spec.Tolerations
	if launcher != nil {
		for key, value := range launcher.Spec.NodeSelector {
			selector[key] = value
		}
		if launcher.Spec.Affinity != nil {
			affinity = launcher.Spec.Affinity
		}
		if len(launcher.Spec.Tolerations) > 0 {
			tolerations = launcher.Spec.Tolerations
		}
	}
	for i := range nodes {
		n := &nodes[i]
		if n.Name == source || n.Spec.Unschedulable || !nodeReady(n) || n.Labels["kubevirt.io/schedulable"] != "true" || !labels.SelectorFromSet(selector).Matches(labels.Set(n.Labels)) || !requiredAffinityMatches(affinity, n) || !toleratesNode(n, tolerations) {
			continue
		}
		ok := true
		for _, vol := range vmi.Spec.Volumes {
			if vol.PersistentVolumeClaim == nil {
				continue
			}
			pvc := pvcByName[vol.PersistentVolumeClaim.ClaimName]
			if pvc == nil || pvc.Spec.VolumeName == "" {
				continue
			}
			if pv := pvByName[pvc.Spec.VolumeName]; pv != nil && !pvNodeAffinityMatches(pv, n) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, n.Name)
		}
	}
	sort.Strings(out)
	return out
}

func toleratesNode(node *corev1.Node, tolerations []corev1.Toleration) bool {
	for i := range node.Spec.Taints {
		taint := &node.Spec.Taints[i]
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		matched := false
		for _, toleration := range tolerations {
			if toleration.Effect != "" && toleration.Effect != taint.Effect {
				continue
			}
			if toleration.Operator == corev1.TolerationOpExists && (toleration.Key == "" || toleration.Key == taint.Key) {
				matched = true
				break
			}
			if toleration.Key == taint.Key && toleration.Value == taint.Value {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func matchingPDBs(pod *corev1.Pod, pdbs []policyv1.PodDisruptionBudget, vmis map[types.UID]*virtv1.VirtualMachineInstance) []PDBInfo {
	var out []PDBInfo
	for i := range pdbs {
		pdb := &pdbs[i]
		if pdb.Namespace != pod.Namespace || pdb.Spec.Selector == nil {
			continue
		}
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil || !selector.Matches(labels.Set(pod.Labels)) {
			continue
		}
		vmi := verifiedLauncherVMI(pod, vmis)
		owned := vmi != nil && controlledByKubeVirtVMI(pdb.OwnerReferences, vmi)
		out = append(out, PDBInfo{Name: pdb.Name, DisruptionsAllowed: pdb.Status.DisruptionsAllowed, CurrentHealthy: pdb.Status.CurrentHealthy, DesiredHealthy: pdb.Status.DesiredHealthy, KubeVirtOwned: owned})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func requiredAffinityMatches(a *corev1.Affinity, n *corev1.Node) bool {
	if a == nil || a.NodeAffinity == nil || a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return true
	}
	return nodeSelectorMatches(a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution, n)
}
func pvNodeAffinityMatches(pv *corev1.PersistentVolume, n *corev1.Node) bool {
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return true
	}
	return nodeSelectorMatches(pv.Spec.NodeAffinity.Required, n)
}
func nodeSelectorMatches(sel *corev1.NodeSelector, n *corev1.Node) bool {
	for _, term := range sel.NodeSelectorTerms {
		matches := true
		for _, req := range append(term.MatchExpressions, term.MatchFields...) {
			value, exists := n.Labels[req.Key]
			if req.Key == "metadata.name" {
				value, exists = n.Name, true
			}
			switch req.Operator {
			case corev1.NodeSelectorOpIn:
				matches = exists && contains(req.Values, value)
			case corev1.NodeSelectorOpNotIn:
				matches = !exists || !contains(req.Values, value)
			case corev1.NodeSelectorOpExists:
				matches = exists
			case corev1.NodeSelectorOpDoesNotExist:
				matches = !exists
			case corev1.NodeSelectorOpGt, corev1.NodeSelectorOpLt:
				matches = numericMatch(value, req.Values, req.Operator)
			}
			if !matches {
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}
func numericMatch(value string, values []string, op corev1.NodeSelectorOperator) bool {
	if len(values) != 1 {
		return false
	}
	var a, b int
	if _, e := fmt.Sscan(value, &a); e != nil {
		return false
	}
	if _, e := fmt.Sscan(values[0], &b); e != nil {
		return false
	}
	if op == corev1.NodeSelectorOpGt {
		return a > b
	}
	return a < b
}
func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
func launcherFor(vmi *virtv1.VirtualMachineInstance, pods []corev1.Pod) *corev1.Pod {
	for i := range pods {
		p := &pods[i]
		if p.Spec.NodeName == vmi.Status.NodeName && launcherOwnedByVMI(p, vmi) {
			return p
		}
	}
	return nil
}

func verifiedLauncherVMI(pod *corev1.Pod, vmis map[types.UID]*virtv1.VirtualMachineInstance) *virtv1.VirtualMachineInstance {
	if len(vmis) == 0 {
		return nil
	}
	createdBy := types.UID(pod.Labels[virtv1.CreatedByLabel])
	if createdBy == "" {
		return nil
	}
	vmi := vmis[createdBy]
	if vmi == nil || !launcherOwnedByVMI(pod, vmi) {
		return nil
	}
	return vmi
}

func launcherOwnedByVMI(pod *corev1.Pod, vmi *virtv1.VirtualMachineInstance) bool {
	activeNode, active := vmi.Status.ActivePods[pod.UID]
	return active && activeNode == pod.Spec.NodeName && pod.Namespace == vmi.Namespace && pod.Labels[virtv1.CreatedByLabel] == string(vmi.UID) && controlledByKubeVirtVMI(pod.OwnerReferences, vmi)
}

func looksLikeLauncher(pod *corev1.Pod) bool {
	if strings.HasPrefix(pod.GenerateName, "virt-launcher-") || pod.Labels[virtv1.AppLabel] == "virt-launcher" || pod.Labels[virtv1.CreatedByLabel] != "" || pod.Annotations[virtv1.DomainAnnotation] != "" {
		return true
	}
	for _, owner := range pod.OwnerReferences {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err == nil && gv.Group == "kubevirt.io" && owner.Kind == "VirtualMachineInstance" {
			return true
		}
	}
	return false
}

func controlledByKubeVirtVMI(owners []metav1.OwnerReference, vmi *virtv1.VirtualMachineInstance) bool {
	for _, owner := range owners {
		group := ""
		if gv, err := schema.ParseGroupVersion(owner.APIVersion); err == nil {
			group = gv.Group
		}
		if owner.Controller != nil && *owner.Controller && group == "kubevirt.io" && owner.Kind == "VirtualMachineInstance" && owner.Name == vmi.Name && owner.UID == vmi.UID {
			return true
		}
	}
	return false
}

func hotplugVolumeNamesFromStatus(vmi *virtv1.VirtualMachineInstance) []string {
	names := map[string]bool{}
	for _, status := range vmi.Status.VolumeStatus {
		if status.HotplugVolume != nil {
			names[status.Name] = true
		}
	}
	return sortedKeys(names)
}

func (c Clients) expectedHotplugVolumeNames(ctx context.Context, vmi *virtv1.VirtualMachineInstance) ([]string, error) {
	names := map[string]bool{}
	for _, name := range hotplugVolumeNamesFromStatus(vmi) {
		names[name] = true
	}
	for _, owner := range vmi.OwnerReferences {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil || gv.Group != "kubevirt.io" || owner.Kind != "VirtualMachine" || owner.UID == "" || owner.Controller == nil || !*owner.Controller {
			continue
		}
		vm, err := c.Virt.VirtualMachine(vmi.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if vm.UID != owner.UID || vm.Spec.Template == nil {
			return nil, fmt.Errorf("owning VirtualMachine identity or template changed")
		}
		templateVolumes := map[string]bool{}
		for _, volume := range vm.Spec.Template.Spec.Volumes {
			templateVolumes[volume.Name] = true
		}
		for _, volume := range vmi.Spec.Volumes {
			if !templateVolumes[volume.Name] {
				names[volume.Name] = true
			}
		}
	}
	return sortedKeys(names), nil
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
func hotplugState(vmi *virtv1.VirtualMachineInstance, pods []corev1.Pod, target string) (int, int) {
	return hotplugStateForNames(vmi, pods, target, hotplugVolumeNamesFromStatus(vmi))
}

func hotplugStateForNames(vmi *virtv1.VirtualMachineInstance, pods []corev1.Pod, target string, expectedNames []string) (int, int) {
	if target == "" {
		target = vmi.Status.NodeName
	}
	expected, ready := len(expectedNames), 0
	podsByUID := map[types.UID]*corev1.Pod{}
	podsByName := map[string]*corev1.Pod{}
	for i := range pods {
		p := &pods[i]
		podsByUID[p.UID] = p
		podsByName[p.Name] = p
	}
	specVolumes := map[string]bool{}
	for _, volume := range vmi.Spec.Volumes {
		specVolumes[volume.Name] = true
	}
	statusByName := map[string]virtv1.VolumeStatus{}
	for _, volume := range vmi.Status.VolumeStatus {
		statusByName[volume.Name] = volume
	}
	for _, name := range expectedNames {
		volume, found := statusByName[name]
		if !found || !specVolumes[name] || volume.HotplugVolume == nil {
			continue
		}
		if volume.Phase != virtv1.VolumeReady {
			continue
		}
		var pod *corev1.Pod
		if target != vmi.Status.NodeName && vmi.Status.MigrationState != nil && vmi.Status.MigrationState.TargetAttachmentPodUID != "" {
			pod = podsByUID[vmi.Status.MigrationState.TargetAttachmentPodUID]
		}
		if pod == nil && volume.HotplugVolume.AttachPodUID != "" {
			pod = podsByUID[volume.HotplugVolume.AttachPodUID]
		}
		if pod == nil && volume.HotplugVolume.AttachPodName != "" {
			pod = podsByName[volume.HotplugVolume.AttachPodName]
		}
		if pod != nil && pod.Spec.NodeName == target && pod.Status.Phase == corev1.PodRunning {
			ready++
		}
	}
	return expected, ready
}
func inferMigrationSource(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[virtv1.EvacuationMigrationAnnotation]
}

func migrationReason(m *virtv1.VirtualMachineInstanceMigration) string {
	for _, c := range m.Status.Conditions {
		if c.Status == corev1.ConditionFalse && c.Message != "" {
			return c.Message
		}
	}
	return ""
}
func controlledByKind(p *corev1.Pod, kind string) bool {
	for _, o := range p.OwnerReferences {
		if o.Controller != nil && *o.Controller && o.Kind == kind {
			return true
		}
	}
	return false
}

func hasController(p *corev1.Pod) bool {
	for _, owner := range p.OwnerReferences {
		if owner.Controller != nil && *owner.Controller {
			return true
		}
	}
	return false
}
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
func stringValue(v *virtv1.EvictionStrategy) string {
	if v == nil {
		return ""
	}
	return string(*v)
}
func valueOr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func key(a, b string) string { return a + "/" + b }

func ObjectRef(kind, namespace, name string, uid types.UID) map[string]string {
	return map[string]string{"kind": kind, "namespace": namespace, "name": name, "uid": string(uid)}
}
