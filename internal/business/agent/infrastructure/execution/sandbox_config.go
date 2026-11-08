package execution

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	corev1 "k8s.io/api/core/v1"
)

// SandboxConfig is trusted deployment configuration. Mounts come from the
// grants Storage issued for the admitted start; conversation identity comes
// from the admitted Work request.
type SandboxConfig struct {
	Image        string
	Resources    corev1.ResourceRequirements
	Environment  map[string]string
	Models       *ModelCatalog
	NetworkDeny  []string
	NetworkAllow []string
	// Lease is how every sandbox is leased: created to expire after its
	// duration, and renewed while its execution wants it.
	Lease domain.LeaseTerms
	// CreationWait bounds one create: longer than the OpenSandbox server's
	// own creation timeout, after which the server deletes a sandbox that
	// never became ready and answers the create. Each execution's creation
	// deadline counts the same wait.
	CreationWait domain.CreationWait
}

func (c SandboxConfig) Validate() error {
	if err := c.CreationWait.Validate(); err != nil {
		return err
	}
	if c.Models == nil {
		return errors.New("agent: model catalog is required")
	}
	for _, key := range sandboxSecretEnvironment {
		if _, ok := c.Environment[key]; ok {
			return errors.New("agent: secrets must not be configured in sandbox environment")
		}
	}
	// Model choice belongs to each Work's Model Configuration, never to the
	// environment shared by every Work.
	for _, key := range modelManagedEnvironment {
		if _, ok := c.Environment[key]; ok {
			return fmt.Errorf("agent: %s comes from the Work's model configuration", key)
		}
	}
	if strings.TrimSpace(c.Image) == "" {
		return errors.New("agent: native image is required")
	}
	// OpenSandbox takes the duration in whole seconds.
	if err := c.Lease.Validate(); err != nil || c.Lease.Duration < time.Second {
		return errors.New("agent: the sandbox lease needs a duration of at least a second and a positive renewal threshold below it")
	}
	// Every Work reaches the platform, through its native API or the Gateway
	// where one is deployed, with the credentials the Credential Proxy adds.
	if _, err := platformAddresses(c.Environment); err != nil {
		return err
	}
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		request, limit := c.Resources.Requests[name], c.Resources.Limits[name]
		if request.Sign() <= 0 || limit.Sign() <= 0 || limit.Cmp(request) < 0 {
			return errors.New("agent: CPU and memory need positive requests and limits at least as large")
		}
	}
	return nil
}
