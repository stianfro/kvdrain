package coordinator

import (
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"

	"github.com/stianfro/kvdrain/internal/kube"
	"github.com/stianfro/kvdrain/internal/state"
)

func TestObserveFailedCountsRelevantUIDOnce(t *testing.T) {
	cutoff := time.Unix(100, 0)
	migration := func(uid string, created time.Time) kube.MigrationInfo {
		return kube.MigrationInfo{Failed: true, Migration: &virtv1.VirtualMachineInstanceMigration{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: uid, UID: types.UID(uid), CreationTimestamp: metav1.NewTime(created)}, Spec: virtv1.VirtualMachineInstanceMigrationSpec{VMIName: "vm"}}}
	}
	items := []kube.MigrationInfo{migration("old", cutoff.Add(-time.Second)), migration("new", cutoff.Add(time.Second)), migration("initial", cutoff.Add(-time.Second))}
	seen := map[string]map[string]bool{}
	initial := map[string]bool{"old": true, "initial": true}
	got := observeFailed(seen, items, initial)
	if len(got) != 1 || len(seen["ns/vm"]) != 1 {
		t.Fatalf("new failures=%d seen=%v", len(got), seen)
	}
	if got := observeFailed(seen, items, initial); len(got) != 0 {
		t.Fatalf("duplicate failures counted: %d", len(got))
	}
}

func TestPreflightRejectsNewUnsafePod(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "late"}}
	snapshot := &kube.Snapshot{Pods: []kube.PodInfo{{Pod: pod, EmptyDir: true}}}
	if err := preflight(snapshot, DrainOptions{}); err == nil {
		t.Fatal("unsafe late pod passed preflight")
	}
}

func TestPreflightForceCannotBypassUnverifiedLauncher(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "lookalike"}}
	snapshot := &kube.Snapshot{Pods: []kube.PodInfo{{Pod: pod, UnverifiedLauncher: true}}}
	if err := preflight(snapshot, DrainOptions{Force: true, DeleteEmptyDirData: true}); err == nil {
		t.Fatal("force bypassed unverified launcher protection")
	}
}

func TestOutputFailureBecomesOperationalError(t *testing.T) {
	c := &Coordinator{Emit: func(state.Event) error { return errors.New("broken pipe") }}
	c.init()
	c.event("run", "ready", "", nil, nil)
	err := c.checkOutput()
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("output error = %v", err)
	}
}

func TestMigrationDuration(t *testing.T) {
	start := metav1.NewTime(time.Unix(10, 0))
	end := metav1.NewTime(time.Unix(42, 0))
	migration := kube.MigrationInfo{Migration: &virtv1.VirtualMachineInstanceMigration{Status: virtv1.VirtualMachineInstanceMigrationStatus{MigrationState: &virtv1.VirtualMachineInstanceMigrationState{StartTimestamp: &start, EndTimestamp: &end}}}}
	got, ok := migrationDuration(migration)
	if !ok || got != 32*time.Second {
		t.Fatalf("duration = %s, %t", got, ok)
	}
}

func TestRealLauncherEvictionMustBeIntercepted(t *testing.T) {
	if err := requireKubeVirtInterception(nil, "ns", "vm"); err == nil {
		t.Fatal("HTTP 200 launcher eviction was accepted")
	}
	expected := apierrors.NewTooManyRequests(`Eviction triggered evacuation of VMI "ns/vm"`, 0)
	if err := requireKubeVirtInterception(expected, "ns", "vm"); err != nil {
		t.Fatalf("expected KubeVirt interception was rejected: %v", err)
	}
}
