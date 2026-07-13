package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestEvictionCarriesPodUIDPrecondition(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: types.UID("pod-uid")}}
	client := fake.NewSimpleClientset(pod.DeepCopy())
	if err := (Clients{Core: client}).EvictPod(context.Background(), pod, -1, false); err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 1 {
		t.Fatalf("actions = %d", len(actions))
	}
	eviction, ok := actions[0].(k8stesting.CreateAction)
	if !ok {
		t.Fatalf("action type = %T", actions[0])
	}
	object, ok := eviction.GetObject().(*policyv1.Eviction)
	if !ok || object.DeleteOptions == nil || object.DeleteOptions.Preconditions == nil || object.DeleteOptions.Preconditions.UID == nil || *object.DeleteOptions.Preconditions.UID != pod.UID {
		t.Fatalf("eviction preconditions = %#v", object)
	}
}

func TestConfirmEvacuationRequiresKubeVirtPDB(t *testing.T) {
	err := (Clients{}).ConfirmEvacuation(context.Background(), VMIInfo{Launcher: &corev1.Pod{}})
	if err == nil {
		t.Fatal("launcher without a verified KubeVirt PDB was accepted")
	}
}

func TestGenericTooManyRequestsIsNotTreatedAsPDBRetry(t *testing.T) {
	if IsRetryablePDB(apierrors.NewTooManyRequests("admission rate limit", 1)) {
		t.Fatal("generic 429 was treated as a PDB retry")
	}
	if !IsRetryablePDB(apierrors.NewTooManyRequests("cannot evict pod as it would violate the pod's disruption budget", 1)) {
		t.Fatal("PDB 429 was not retryable")
	}
}

func TestDrainLeaseFailsClosedForAnotherHolder(t *testing.T) {
	clients := Clients{Core: fake.NewSimpleClientset()}
	if err := clients.AcquireDrainLease(context.Background(), "node-a", "run-a", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := clients.AcquireDrainLease(context.Background(), "node-a", "run-b", time.Minute); err == nil || !strings.Contains(err.Error(), "already being drained") {
		t.Fatalf("concurrent lease result = %v", err)
	}
	if err := clients.ReleaseDrainLease(context.Background(), "node-a", "run-a"); err != nil {
		t.Fatal(err)
	}
	if err := clients.AcquireDrainLease(context.Background(), "node-a", "run-b", time.Minute); err != nil {
		t.Fatalf("lease was not available after release: %v", err)
	}
}

func TestRollbackCordonRequiresNodeUIDAndRunOwner(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", UID: "node-uid", ResourceVersion: "9", Annotations: map[string]string{CordonOwnerAnnotation: "run-a"}}, Spec: corev1.NodeSpec{Unschedulable: true}}
	clients := Clients{Core: fake.NewSimpleClientset(node)}
	if err := clients.RollbackCordon(context.Background(), node.Name, "run-b", node.UID); err == nil {
		t.Fatal("another run was allowed to remove the cordon")
	}
	if err := clients.RollbackCordon(context.Background(), node.Name, "run-a", "replacement-uid"); err == nil {
		t.Fatal("a replacement node was allowed to inherit rollback")
	}
	if err := clients.RollbackCordon(context.Background(), node.Name, "run-a", node.UID); err != nil {
		t.Fatalf("owned cordon could not be restored: %v", err)
	}
	got, err := clients.Core.CoreV1().Nodes().Get(context.Background(), node.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.Unschedulable || got.Annotations[CordonOwnerAnnotation] != "" {
		t.Fatalf("node was not restored: %#v", got)
	}
}
