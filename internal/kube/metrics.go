package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stianfro/kvdrain/internal/metrics"
)

// SourceMetrics scrapes the source node virt-handler through the Kubernetes pod proxy.
// Callers treat errors as an unavailable optional signal.
func (c Clients) SourceMetrics(ctx context.Context, node string) (map[string]metrics.Transfer, error) {
	pods, err := c.Core.CoreV1().Pods("").List(ctx, metav1.ListOptions{LabelSelector: "kubevirt.io=virt-handler", FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return nil, fmt.Errorf("list virt-handler pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("virt-handler pod not found on node %s", node)
	}
	pod := &pods.Items[0]
	raw, err := c.Core.CoreV1().RESTClient().Get().Namespace(pod.Namespace).Resource("pods").Name(pod.Name + ":8443").SubResource("proxy").Suffix("metrics").DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("scrape virt-handler metrics: %w", err)
	}
	return metrics.ParseAll(string(raw)), nil
}
