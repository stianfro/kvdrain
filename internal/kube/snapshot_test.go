package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
)

func TestClassifyPod(t *testing.T) {
	controller := true
	tests := []struct {
		name                              string
		pod                               corev1.Pod
		ignored, managed, empty, launcher bool
	}{
		{name: "mirror", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{corev1.MirrorPodAnnotationKey: "x"}}}, ignored: true},
		{name: "daemonset", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Controller: &controller}}}}, ignored: true},
		{name: "completed", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}, ignored: true},
		{name: "managed emptydir", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Controller: &controller}}}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{{VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}}}, managed: true, empty: true},
		{name: "custom controller", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "CustomController", Controller: &controller}}}}, managed: true},
		{name: "non-controller owner is unmanaged", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{Kind: "CustomOwner"}}}}},
		{name: "spoofed launcher annotation", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{virtv1.DomainAnnotation: "vm"}}}},
		{name: "spoofed launcher generate name", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: "virt-launcher-fake-"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyPod(&tt.pod)
			if got.Ignored != tt.ignored || got.Managed != tt.managed || got.EmptyDir != tt.empty || got.Launcher != tt.launcher {
				t.Fatalf("classification = %+v", got)
			}
		})
	}
}

func TestHotplugUsesExactAttachmentIdentity(t *testing.T) {
	uid := types.UID("attach-1")
	vmi := &virtv1.VirtualMachineInstance{Spec: virtv1.VirtualMachineInstanceSpec{Volumes: []virtv1.Volume{{Name: "hot"}}}, Status: virtv1.VirtualMachineInstanceStatus{NodeName: "target", VolumeStatus: []virtv1.VolumeStatus{{Name: "hot", Phase: virtv1.VolumeReady, HotplugVolume: &virtv1.HotplugVolumeStatus{AttachPodUID: uid, AttachPodName: "attachment"}}}}}
	pods := []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "attachment-like", UID: "other"}, Spec: corev1.PodSpec{NodeName: "target"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}, {ObjectMeta: metav1.ObjectMeta{Name: "attachment", UID: uid}, Spec: corev1.PodSpec{NodeName: "target"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}}
	expected, ready := hotplugState(vmi, pods, "target")
	if expected != 1 || ready != 1 {
		t.Fatalf("expected %d ready %d", expected, ready)
	}
}

func TestLauncherRequiresExactKubeVirtControllerAndVMIUID(t *testing.T) {
	controller := true
	vmi := &virtv1.VirtualMachineInstance{ObjectMeta: metav1.ObjectMeta{Name: "vm", Namespace: "ns", UID: "vmi-1"}, Status: virtv1.VirtualMachineInstanceStatus{ActivePods: map[types.UID]string{"pod-1": "source"}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "virt-launcher-vm", Namespace: "ns", UID: "pod-1", Labels: map[string]string{virtv1.CreatedByLabel: "vmi-1"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "kubevirt.io/v1", Kind: "VirtualMachineInstance", Name: "vm", UID: "vmi-1", Controller: &controller}}}, Spec: corev1.PodSpec{NodeName: "source"}}
	got := classifyPod(pod, nil, nil, map[types.UID]*virtv1.VirtualMachineInstance{"vmi-1": vmi})
	if !got.Launcher {
		t.Fatal("verified launcher was treated as a normal pod")
	}
	pod.OwnerReferences[0].UID = "attacker"
	got = classifyPod(pod, nil, nil, map[types.UID]*virtv1.VirtualMachineInstance{"vmi-1": vmi})
	if got.Launcher {
		t.Fatal("launcher with mismatched owner UID was trusted")
	}
	if got.Managed {
		t.Fatal("unverified launcher lookalike was treated as controller-managed")
	}
	if !got.UnverifiedLauncher {
		t.Fatal("unverified launcher lookalike was not marked as a hard blocker")
	}
}

func TestEligibleTargetsHonorsSelectorsAndPVNodeAffinity(t *testing.T) {
	vmi := &virtv1.VirtualMachineInstance{ObjectMeta: metav1.ObjectMeta{Namespace: "ns"}, Spec: virtv1.VirtualMachineInstanceSpec{NodeSelector: map[string]string{"zone": "b"}, Volumes: []virtv1.Volume{{Name: "disk", VolumeSource: virtv1.VolumeSource{PersistentVolumeClaim: &virtv1.PersistentVolumeClaimVolumeSource{PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{ClaimName: "claim"}}}}}}}
	nodes := []corev1.Node{readyNode("source", "a"), readyNode("target-b", "b"), readyNode("target-c", "c")}
	pvcs := []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "ns"}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "pv"}}}
	pvs := []corev1.PersistentVolume{{ObjectMeta: metav1.ObjectMeta{Name: "pv"}, Spec: corev1.PersistentVolumeSpec{NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "zone", Operator: corev1.NodeSelectorOpIn, Values: []string{"b"}}}}}}}}}}
	got := eligibleTargets(vmi, nil, nodes, pvcs, pvs, "source")
	if len(got) != 1 || got[0] != "target-b" {
		t.Fatalf("targets = %v", got)
	}
}
func readyNode(name, zone string) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"zone": zone, "kubevirt.io/schedulable": "true"}}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}}
}
