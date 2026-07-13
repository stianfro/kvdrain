package kube

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestVirtHandlerIdentityBindsPodDaemonSetAndKubeVirtVersion(t *testing.T) {
	controller := true
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "virt-handler-a", Labels: map[string]string{"kubevirt.io": "virt-handler"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "virt-handler", UID: "ds-uid", Controller: &controller}}}}
	daemonSet := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "virt-handler", UID: "ds-uid", Labels: map[string]string{"kubevirt.io": "virt-handler", "app.kubernetes.io/managed-by": "virt-operator", "app.kubernetes.io/version": "v1.6.3"}, Annotations: map[string]string{"kubevirt.io/install-strategy-version": "v1.6.3"}}}
	if !verifiedVirtHandler(pod, daemonSet, "v1.6.3") {
		t.Fatal("valid virt-handler identity was rejected")
	}
	daemonSet.UID = "attacker"
	if verifiedVirtHandler(pod, daemonSet, "v1.6.3") {
		t.Fatal("DaemonSet UID mismatch was accepted")
	}
	daemonSet.UID = "ds-uid"
	if verifiedVirtHandler(pod, daemonSet, "v1.7.0") {
		t.Fatal("KubeVirt version mismatch was accepted")
	}
}
