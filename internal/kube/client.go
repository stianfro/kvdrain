package kube

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"kubevirt.io/client-go/kubecli"
)

type ConfigOptions struct{ Kubeconfig, Context string }

func NewConfig(o ConfigOptions) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if o.Kubeconfig != "" {
		rules.ExplicitPath = o.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: o.Context}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	cfg.UserAgent = "kvdrain"
	return cfg, nil
}
func NewClients(cfg *rest.Config) (Clients, error) {
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return Clients{}, fmt.Errorf("create Kubernetes client: %w", err)
	}
	virt, err := kubecli.GetKubevirtClientFromRESTConfig(cfg)
	if err != nil {
		return Clients{}, fmt.Errorf("create KubeVirt client: %w", err)
	}
	return Clients{Core: core, Virt: virt}, nil
}
