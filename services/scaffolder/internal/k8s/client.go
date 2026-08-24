// Package k8s builds the clients used to talk to the cluster.
package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients bundles the two clients this service needs.
type Clients struct {
	Dynamic dynamic.Interface
	Typed   kubernetes.Interface
	// Host is the API server the clients are pointed at, for logging.
	Host string
}

// New builds the clients, preferring in-cluster credentials and falling back to
// a kubeconfig so the service can be run from a laptop against kind.
//
// KUBECONFIG is honoured, and KUBE_CONTEXT can pin a specific context: this
// service must never end up talking to whatever cluster happens to be current.
func New() (Clients, error) {
	cfg, err := restConfig()
	if err != nil {
		return Clients{}, err
	}

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return Clients{}, fmt.Errorf("building the dynamic client: %w", err)
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return Clients{}, fmt.Errorf("building the typed client: %w", err)
	}
	return Clients{Dynamic: dyn, Typed: typed, Host: cfg.Host}, nil
}

func restConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path := os.Getenv("KUBECONFIG"); path != "" {
		rules.ExplicitPath = path
	} else if home, err := os.UserHomeDir(); err == nil {
		rules.ExplicitPath = filepath.Join(home, ".kube", "config")
	}

	overrides := &clientcmd.ConfigOverrides{}
	if context := os.Getenv("KUBE_CONTEXT"); context != "" {
		overrides.CurrentContext = context
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("no in-cluster credentials and no usable kubeconfig: %w", err)
	}
	return cfg, nil
}
