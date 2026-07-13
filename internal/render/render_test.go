package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stianfro/kvdrain/internal/state"
)

func TestJSONEventEnvelopeAndTransitionSuppression(t *testing.T) {
	var b bytes.Buffer
	r := New(&b, Options{JSON: true})
	e := state.Event{APIVersion: state.APIVersion, Kind: "Event", Time: time.Unix(1, 0).UTC(), RunID: "run", Type: "pod", Node: "node", Object: &state.ObjectRef{Kind: "Pod", Namespace: "ns", Name: "pod"}, State: "waiting", Message: "PDB"}
	if err := r.Event(e); err != nil {
		t.Fatal(err)
	}
	if err := r.Event(e); err != nil {
		t.Fatal(err)
	}
	var got state.Event
	if err := json.Unmarshal(bytes.TrimSpace(b.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if got.APIVersion != "kvdrain.io/v1alpha1" || got.Kind != "Event" || got.Object.Name != "pod" {
		t.Fatalf("event = %+v", got)
	}
	if bytes.Count(b.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("expected one NDJSON line: %q", b.String())
	}
}

func TestLiveRowCombinesMigrationTransferAndHotplug(t *testing.T) {
	r := &Renderer{rows: map[string]liveRow{}, nodes: map[string]bool{}}
	object := &state.ObjectRef{Kind: "VirtualMachineInstanceMigration", Namespace: "ns", Name: "migration"}
	r.update(state.Event{Type: "migration", State: "running", Object: object, Details: map[string]any{"vmi": "vm", "target": "node-b", "retry": 1}})
	r.update(state.Event{Type: "xfer", State: "observed", Object: &state.ObjectRef{Kind: "VirtualMachineInstance", Namespace: "ns", Name: "vm"}, Details: map[string]any{"processedBytes": float64(1024), "totalBytes": float64(2048), "memoryRateBytes": float64(512)}})
	r.update(state.Event{Type: "hotplug", State: "ready", Object: &state.ObjectRef{Kind: "VirtualMachineInstance", Namespace: "ns", Name: "vm"}, Details: map[string]any{"expected": 2, "ready": 2}})

	row := r.rows["ns/vm"]
	if row.phase != "Running" || row.target != "node-b" || row.retry != "1" || row.hotplug != "2/2 ready" {
		t.Fatalf("row = %+v", row)
	}
	if !strings.Contains(row.transfer, "1.0 KiB/2.0 KiB") {
		t.Fatalf("transfer = %q", row.transfer)
	}
}

func TestHumanOutputStripsTerminalControlsAndEscapesLines(t *testing.T) {
	message := "before\x1b]52;c;clipboard\a\nafter\u009b31m\u202ereversed"
	got := formatLine(state.Event{Type: "pod", State: "blocked", Message: message}, false)
	if strings.ContainsAny(got, "\x1b\a\u009b\u202e") || !strings.Contains(got, `\nafter`) || strings.Count(got, "\n") != 0 {
		t.Fatalf("unsafe human output: %q", got)
	}
}
