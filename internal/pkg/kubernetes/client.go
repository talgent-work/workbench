package kubernetes

import (
	"github.com/samber/oops"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Config struct {
	KubeconfigPath string `json:"kubeconfig-path" yaml:"kubeconfig-path" toml:"kubeconfig-path"`
}

// NewRESTConfig resolves a rest.Config from cfg.KubeconfigPath when set,
// otherwise it falls back to in-cluster configuration. Exported so tests
// (envtest) can inject a rest.Config directly via NewClientsetFromRESTConfig.
func NewRESTConfig(cfg Config) (*rest.Config, error) {
	if cfg.KubeconfigPath != "" {
		c, err := clientcmd.BuildConfigFromFlags("", cfg.KubeconfigPath)
		if err != nil {
			return nil, oops.Wrap(err)
		}
		return c, nil
	}
	c, err := rest.InClusterConfig()
	if err != nil {
		return nil, oops.Wrap(err)
	}
	return c, nil
}

// NewClientset builds a typed kubernetes.Interface for the resolved REST config.
func NewClientset(cfg Config) (kubernetes.Interface, error) {
	rc, err := NewRESTConfig(cfg)
	if err != nil {
		return nil, err
	}
	return NewClientsetFromRESTConfig(rc)
}

// NewClientsetFromRESTConfig wraps kubernetes.NewForConfig and is the seam
// envtest uses to inject its apiserver-local rest.Config.
func NewClientsetFromRESTConfig(rc *rest.Config) (kubernetes.Interface, error) {
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, oops.Wrap(err)
	}
	return cs, nil
}

// NewDynamicClient builds an untyped client for resources Talgent does not
// generate types for, such as OpenSandbox custom resources.
func NewDynamicClient(cfg Config) (dynamic.Interface, error) {
	rc, err := NewRESTConfig(cfg)
	if err != nil {
		return nil, err
	}
	client, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, oops.Wrap(err)
	}
	return client, nil
}
