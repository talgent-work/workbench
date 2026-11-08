package execution

import (
	"context"
	"errors"

	"github.com/samber/oops"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// sandboxIDLabel is the label OpenSandbox sets on each sandbox's Pod naming
// the sandbox it belongs to.
const sandboxIDLabel = "opensandbox.io/id"

// SandboxPods reads the Pods in the sandbox namespace that carry an
// execution's ownership label. A sandbox's Pod outlives its BatchSandbox until
// it terminates and can still write the Work's directories, so an
// environment is gone only once neither remains (ADR 0047, Release waits for
// the Pod). It only reads.
type SandboxPods struct {
	client    kubernetes.Interface
	namespace string
}

func NewSandboxPods(client kubernetes.Interface, namespace string) (*SandboxPods, error) {
	if client == nil || namespace == "" {
		return nil, errors.New("agent: sandbox Pod observation needs a client and the sandbox namespace")
	}
	return &SandboxPods{client: client, namespace: namespace}, nil
}

// running lists the sandbox identities of the execution's Pods that have not
// terminated, each once.
func (p *SandboxPods) running(ctx context.Context, owner SandboxOwner) ([]string, error) {
	selector := labels.Set{sandboxOwnershipLabel: correlation(owner)[sandboxOwnershipLabel]}.AsSelector().String()
	pods, err := p.client.CoreV1().Pods(p.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, oops.With("operation", "list_sandbox_pods").Wrap(err)
	}
	var ids []string
	seen := map[string]bool{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		id := pod.Labels[sandboxIDLabel]
		if id == "" {
			id = pod.Name
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
