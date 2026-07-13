package render

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stianfro/kvdrain/internal/state"
	"golang.org/x/term"
)

type Options struct {
	JSON  bool
	NoTTY bool
	Wide  bool
}

type liveRow struct {
	name, phase, target, transfer, retry, hotplug, message, node string
	vm                                                           bool
}

type Renderer struct {
	out     io.Writer
	opts    Options
	tty     bool
	reducer *state.Reducer
	mu      sync.Mutex
	started time.Time
	rows    map[string]liveRow
	nodes   map[string]bool
}

func New(out io.Writer, opts Options) *Renderer {
	tty := false
	if file, ok := out.(*os.File); ok && !opts.NoTTY && !opts.JSON {
		tty = term.IsTerminal(int(file.Fd()))
	}
	return &Renderer{
		out: out, opts: opts, tty: tty, reducer: state.NewReducer(), started: time.Now(),
		rows: map[string]liveRow{}, nodes: map[string]bool{},
	}
}

func (r *Renderer) Event(e state.Event) error {
	if !r.reducer.Accept(e) {
		return nil
	}
	if r.opts.JSON {
		return json.NewEncoder(r.out).Encode(e)
	}
	if r.tty {
		return r.redraw(e)
	}
	_, err := fmt.Fprintln(r.out, formatLine(e, r.opts.Wide))
	return err
}

func (r *Renderer) redraw(e state.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.Node != "" {
		r.nodes[e.Node] = true
	}
	r.update(e)

	keys := make([]string, 0, len(r.rows))
	for key := range r.rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	total, drained := 0, 0
	for _, row := range r.rows {
		if !row.vm {
			continue
		}
		total++
		if strings.EqualFold(row.phase, "succeeded") || strings.EqualFold(row.phase, "drained") {
			drained++
		}
	}

	var view strings.Builder
	fmt.Fprintf(&view, "NODE %-24s drained %d/%d VMIs   elapsed %s\n", r.nodeName(), drained, total, shortDuration(time.Since(r.started)))
	view.WriteString("NAMESPACE/VMI                    PHASE        TARGET                   XFER                 RETRY  HP\n")
	for _, key := range keys {
		row := r.rows[key]
		if !row.vm {
			continue
		}
		message := ""
		if r.opts.Wide && row.message != "" {
			message = "  " + row.message
		}
		fmt.Fprintf(&view, "%-32s %-12s %-24s %-20s %-6s %s%s\n",
			row.name, valueOr(row.phase, "Pending"), valueOr(row.target, "N/A"), valueOr(row.transfer, "N/A"), valueOr(row.retry, "0"), valueOr(row.hotplug, "N/A"), message)
	}
	for _, key := range keys {
		row := r.rows[key]
		if row.vm {
			continue
		}
		fmt.Fprintf(&view, "POD %-28s %-12s %s\n", row.name, row.phase, row.message)
	}
	_, err := fmt.Fprintf(r.out, "\x1b[H\x1b[2J%s", view.String())
	return err
}

func (r *Renderer) update(e state.Event) {
	if e.Object == nil {
		return
	}
	name := strings.TrimPrefix(e.Object.Namespace+"/"+e.Object.Name, "/")
	vm := e.Object.Kind == "VirtualMachineInstance"
	if e.Type == "migration" {
		if vmi, ok := e.Details["vmi"].(string); ok && vmi != "" {
			name = strings.TrimPrefix(e.Object.Namespace+"/"+vmi, "/")
		}
		vm = true
	}
	key := name
	if !vm {
		key = "pod:" + name
	}
	row := r.rows[key]
	row.name, row.vm, row.node = name, vm, e.Node
	row.message = e.Message
	switch e.Type {
	case "migration":
		row.phase = title(e.State)
		row.target = stringDetail(e.Details, "target")
		row.retry = numberString(e.Details["retry"])
	case "xfer":
		row.transfer = transferString(e.Details)
	case "hotplug":
		expected, ready := number(e.Details["expected"]), number(e.Details["ready"])
		row.hotplug = fmt.Sprintf("%d/%d", int64(ready), int64(expected))
		if expected == ready {
			row.hotplug += " ready"
		}
	case "vmi":
		if row.phase == "" || e.State == "blocked" || e.State == "triggered" {
			row.phase = title(e.State)
		}
		if expected, ok := e.Details["hotplugExpected"]; ok && number(expected) > 0 {
			row.hotplug = fmt.Sprintf("%d/%d", int64(number(e.Details["hotplugReady"])), int64(number(expected)))
		}
	default:
		row.phase = title(e.State)
	}
	r.rows[key] = row
}

func (r *Renderer) nodeName() string {
	if len(r.nodes) == 0 {
		return "cluster"
	}
	if len(r.nodes) > 1 {
		return "multiple"
	}
	for node := range r.nodes {
		return node
	}
	return "cluster"
}

func formatLine(e state.Event, wide bool) string {
	object := ""
	if e.Object != nil {
		object = strings.TrimPrefix(e.Object.Namespace+"/"+e.Object.Name, "/")
	}
	line := fmt.Sprintf("%-12s %-18s %-32s %s", e.State, e.Type, object, e.Message)
	if wide && len(e.Details) > 0 {
		details, _ := json.Marshal(e.Details)
		line += " " + string(details)
	}
	return strings.TrimRight(line, " ")
}

func transferString(details map[string]any) string {
	processed, hasProcessed := details["processedBytes"]
	total, hasTotal := details["totalBytes"]
	if !hasProcessed || !hasTotal {
		return "N/A"
	}
	out := humanBytes(number(processed)) + "/" + humanBytes(number(total))
	if rate, ok := details["memoryRateBytes"]; ok && rate != nil {
		out += " " + humanBytes(number(rate)) + "/s"
	} else if rate, ok := details["diskRateBytes"]; ok && rate != nil {
		out += " " + humanBytes(number(rate)) + "/s"
	}
	return out
}

func humanBytes(value float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	unit := 0
	for math.Abs(value) >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

func number(value any) float64 {
	switch value := value.(type) {
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	case float32:
		return float64(value)
	case float64:
		return value
	case json.Number:
		result, _ := value.Float64()
		return result
	default:
		return 0
	}
}

func numberString(value any) string { return fmt.Sprintf("%d", int64(number(value))) }

func stringDetail(details map[string]any, key string) string {
	value, _ := details[key].(string)
	return value
}

func title(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func shortDuration(value time.Duration) string {
	value = value.Round(time.Second)
	if value < time.Second {
		return "0s"
	}
	return value.String()
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
