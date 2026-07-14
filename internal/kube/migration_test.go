package kube

import (
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	virtv1 "kubevirt.io/api/core/v1"
)

func TestMigrationPhasesTreatUnknownNonFinalAsActive(t *testing.T) {
	for _, phase := range []virtv1.VirtualMachineInstanceMigrationPhase{virtv1.MigrationPending, "WaitingForSync", "Synchronizing"} {
		migration := &virtv1.VirtualMachineInstanceMigration{Status: virtv1.VirtualMachineInstanceMigrationStatus{Phase: phase}}
		if !MigrationInfoFor(migration).Active {
			t.Errorf("phase %q was not active", phase)
		}
	}
	for _, phase := range []virtv1.VirtualMachineInstanceMigrationPhase{virtv1.MigrationSucceeded, virtv1.MigrationFailed} {
		migration := &virtv1.VirtualMachineInstanceMigration{Status: virtv1.VirtualMachineInstanceMigrationStatus{Phase: phase}}
		if MigrationInfoFor(migration).Active {
			t.Errorf("phase %q was active", phase)
		}
	}
}

func TestKubeVirtEvacuationResponseRequiresExactVMI(t *testing.T) {
	accepted := apierrors.NewTooManyRequests(`admission webhook denied the request: Eviction triggered evacuation of VMI "ns/vm-a"`, 0)
	if !IsKubeVirtEvacuationAccepted(accepted, "ns", "vm-a") {
		t.Fatal("exact KubeVirt response was rejected")
	}
	for _, err := range []error{
		apierrors.NewTooManyRequests(`Cannot evict pod vm-a as it would violate its disruption budget`, 0),
		apierrors.NewTooManyRequests(`Eviction triggered evacuation of VMI "ns/vm-b"`, 0),
	} {
		if IsKubeVirtEvacuationAccepted(err, "ns", "vm-a") {
			t.Fatalf("unrelated response was accepted: %v", err)
		}
	}
}
