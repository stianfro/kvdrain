package metrics

import "testing"

func TestParseOldAndNewTransferRates(t *testing.T) {
	in := `kubevirt_vmi_migration_data_processed_bytes{namespace="ns",name="vm",node="n1"} 10
kubevirt_vmi_migration_data_remaining_bytes{namespace="ns",name="vm",node="n1"} 20
kubevirt_vmi_migration_disk_transfer_rate_bytes{namespace="ns",name="vm",node="n1"} 30
kubevirt_vmi_migration_memory_transfer_rate_bytes_per_second{namespace="ns",name="vm",node="n1"} 40
`
	got := ParseAll(in)["ns/vm"]
	if got.Processed == nil || *got.Processed != 10 || got.Remaining == nil || *got.Remaining != 20 || got.DiskRate == nil || *got.DiskRate != 30 || got.MemoryRate == nil || *got.MemoryRate != 40 {
		t.Fatalf("parse = %+v", got)
	}
	if got.Namespace != "ns" || got.Name != "vm" || got.Node != "n1" {
		t.Fatalf("labels = %+v", got)
	}
}

func TestParseRejectsNonFiniteValues(t *testing.T) {
	got := ParseAll("kubevirt_vmi_migration_data_processed_bytes NaN\nkubevirt_vmi_migration_data_remaining_bytes +Inf\n")
	if len(got) != 0 {
		t.Fatalf("non-finite metrics were retained: %#v", got)
	}
}
