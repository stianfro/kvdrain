package metrics

import (
	"bufio"
	"math"
	"strconv"
	"strings"
)

type Transfer struct {
	Namespace  string
	Name       string
	Node       string
	Processed  *float64
	Remaining  *float64
	DiskRate   *float64
	MemoryRate *float64
}

// Parse accepts both the original disk rate and newer memory rate metric names.
func Parse(input string) Transfer {
	all := ParseAll(input)
	if out, ok := all[""]; ok {
		return out
	}
	for _, out := range all {
		return out
	}
	return Transfer{}
}

func ParseAll(input string) map[string]Transfer {
	out := map[string]Transfer{}
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		metric := fields[0]
		name := metric
		labels := ""
		if i := strings.IndexByte(metric, '{'); i >= 0 {
			name = metric[:i]
			if end := strings.LastIndexByte(metric, '}'); end > i {
				labels = metric[i+1 : end]
			}
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		namespace := metricLabel(labels, "namespace")
		vmiName := metricLabel(labels, "name")
		key := namespace + "/" + vmiName
		if namespace == "" && vmiName == "" {
			key = ""
		}
		transfer := out[key]
		transfer.Namespace = namespace
		transfer.Name = vmiName
		transfer.Node = metricLabel(labels, "node")
		switch {
		case strings.HasSuffix(name, "migration_data_processed_bytes") || strings.HasSuffix(name, "migration_processed_bytes"):
			transfer.Processed = ptr(v)
		case strings.HasSuffix(name, "migration_data_remaining_bytes") || strings.HasSuffix(name, "migration_remaining_bytes"):
			transfer.Remaining = ptr(v)
		case strings.HasSuffix(name, "migration_disk_transfer_rate_bytes") || strings.HasSuffix(name, "migration_disk_transfer_rate_bytes_per_second"):
			transfer.DiskRate = ptr(v)
		case strings.HasSuffix(name, "migration_memory_transfer_rate_bytes") || strings.HasSuffix(name, "migration_memory_transfer_rate_bytes_per_second"):
			transfer.MemoryRate = ptr(v)
		default:
			continue
		}
		out[key] = transfer
	}
	return out
}

func metricLabel(labels, name string) string {
	prefix := name + "=\""
	for _, part := range strings.Split(labels, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, prefix) && strings.HasSuffix(part, "\"") {
			return strings.TrimSuffix(strings.TrimPrefix(part, prefix), "\"")
		}
	}
	return ""
}

func ptr(v float64) *float64 { return &v }
