package kube

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	virtv1 "kubevirt.io/api/core/v1"

	"github.com/stianfro/kvdrain/internal/metrics"
)

// SourceMetrics scrapes the source node virt-handler through the Kubernetes pod proxy.
// Callers treat errors as an unavailable optional signal.
func (c Clients) SourceMetrics(ctx context.Context, node string) (map[string]metrics.Transfer, error) {
	scrapeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	kubeVirts, err := c.Virt.KubeVirt(corev1.NamespaceAll).List(scrapeCtx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("discover KubeVirt namespace: %w", err)
	}
	var namespaces []string
	versions := map[string]string{}
	for _, installation := range kubeVirts.Items {
		if installation.Status.Phase == virtv1.KubeVirtPhaseDeployed {
			namespaces = append(namespaces, installation.Namespace)
			versions[installation.Namespace] = installation.Status.ObservedKubeVirtVersion
		}
	}
	if len(namespaces) == 0 {
		return nil, fmt.Errorf("discover KubeVirt namespace: no deployed KubeVirt installation found")
	}
	sort.Strings(namespaces)
	var namespace string
	var pod *corev1.Pod
	for _, candidateNamespace := range namespaces {
		pods, listErr := c.Core.CoreV1().Pods(candidateNamespace).List(scrapeCtx, metav1.ListOptions{LabelSelector: "kubevirt.io=virt-handler", FieldSelector: "spec.nodeName=" + node})
		if listErr != nil {
			continue
		}
		sort.Slice(pods.Items, func(i, j int) bool { return pods.Items[i].Name < pods.Items[j].Name })
		for i := range pods.Items {
			candidate := &pods.Items[i]
			owner := metav1.GetControllerOf(candidate)
			if owner == nil {
				continue
			}
			daemonSet, getErr := c.Core.AppsV1().DaemonSets(candidateNamespace).Get(scrapeCtx, owner.Name, metav1.GetOptions{})
			expectedVersion := versions[candidateNamespace]
			if getErr == nil && verifiedVirtHandler(candidate, daemonSet, expectedVersion) {
				namespace, pod = candidateNamespace, candidate
				break
			}
		}
		if pod != nil {
			break
		}
	}
	if pod == nil {
		return nil, fmt.Errorf("verified virt-handler DaemonSet pod not found on node %s", node)
	}
	stream, err := c.Core.CoreV1().RESTClient().Get().Namespace(pod.Namespace).Resource("pods").Name(pod.Name + ":8443").SubResource("proxy").Suffix("metrics").Stream(scrapeCtx)
	if err != nil {
		return nil, fmt.Errorf("scrape virt-handler metrics: %w", err)
	}
	defer func() { _ = stream.Close() }()
	const maxMetricsBytes = 8 << 20
	raw, err := io.ReadAll(io.LimitReader(stream, maxMetricsBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read virt-handler metrics: %w", err)
	}
	if len(raw) > maxMetricsBytes {
		return nil, fmt.Errorf("virt-handler metrics response exceeds %d bytes", maxMetricsBytes)
	}
	current, err := c.Core.CoreV1().Pods(namespace).Get(scrapeCtx, pod.Name, metav1.GetOptions{})
	if err != nil || current.UID != pod.UID {
		return nil, fmt.Errorf("virt-handler pod identity changed during scrape")
	}
	return metrics.ParseAll(string(raw)), nil
}

func verifiedVirtHandler(pod *corev1.Pod, daemonSet *appsv1.DaemonSet, expectedVersion string) bool {
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.Kind != "DaemonSet" || owner.Name != "virt-handler" || owner.UID == "" || daemonSet.Name != owner.Name || daemonSet.UID != owner.UID {
		return false
	}
	gv, err := schema.ParseGroupVersion(owner.APIVersion)
	return err == nil && gv.Group == "apps" && expectedVersion != "" && pod.Labels["kubevirt.io"] == "virt-handler" && daemonSet.Labels["kubevirt.io"] == "virt-handler" && daemonSet.Labels["app.kubernetes.io/managed-by"] == "virt-operator" && daemonSet.Labels["app.kubernetes.io/version"] == expectedVersion && daemonSet.Annotations["kubevirt.io/install-strategy-version"] == expectedVersion
}
