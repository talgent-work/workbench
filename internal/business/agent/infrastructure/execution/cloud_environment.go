package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// cloudEnvironment is the Cloud realization's environment authority: an
// OpenSandbox sandbox per allocation, created from its fixed specification,
// with the Work's credentials delivered to the sandbox's Vault. Creation
// that cannot be confirmed may leave surplus sandboxes, which it prunes, and
// it observes a retired sandbox's absence itself. Its Works say whether a
// Work reclaims its run beyond reach.
type cloudEnvironment struct {
	sandboxes   *Sandboxes
	credentials *SandboxCredentials
	works       *HeldWorks
}

var (
	_ environmentAuthority = cloudEnvironment{}
	_ credentialDelivery   = cloudEnvironment{}
	_ surplusEnvironments  = cloudEnvironment{}
	_ observedCompute      = cloudEnvironment{}
	_ unreachedReclamation = cloudEnvironment{}
)

func (cloudEnvironment) launchLayout() launchLayout { return sandboxLayout }

func (c cloudEnvironment) specify(ctx context.Context, request domain.StartExecutionRequest, mounts domain.SandboxMounts, owner Allocation, token, credential string) (environmentSpecification, error) {
	spec, err := c.sandboxes.specification(ctx, request, mounts, owner, token, credential)
	if err != nil {
		return environmentSpecification{}, err
	}
	document, err := json.Marshal(spec)
	if err != nil {
		return environmentSpecification{}, oops.Wrap(err)
	}
	authorizations, err := usedAuthorizations(request.AuthorizationSelection)
	return environmentSpecification{document: document, authorizations: authorizations}, err
}

// sandboxSpec is the allocation's validated sandbox specification.
func (cloudEnvironment) sandboxSpec(record allocationRecord) (Allocation, sdk.CreateSandboxRequest, error) {
	owner := Allocation{WorkID: record.SessionID, ID: record.ID}
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return owner, spec, oops.With("operation", "decode_allocation_spec").Wrap(err)
	}
	if err := owner.validate(); err != nil {
		return owner, spec, err
	}
	if spec.Metadata[sandboxSpecLabel] == "" || spec.Env["EXECD_ACCESS_TOKEN"] == "" {
		return owner, spec, domain.ErrExecutionRequest
	}
	return owner, spec, nil
}

// claim freezes the Work's repository authorization with the submission, so
// a waiting preparation does not suppress durable registration of the start.
func (c cloudEnvironment) claim(ctx context.Context, record allocationRecord) ([]byte, error) {
	if _, _, err := c.sandboxSpec(record); err != nil {
		return nil, err
	}
	request, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return nil, err
	}
	return c.credentials.plan(ctx, request.HomeBinding.OwnerID, record.SessionID)
}

func (c cloudEnvironment) create(ctx context.Context, record allocationRecord) (string, error) {
	owner, spec, err := c.sandboxSpec(record)
	if err != nil {
		return "", err
	}
	return c.sandboxes.submit(ctx, owner, spec)
}

// find looks the sandbox up; an empty lookup cannot prove an in-flight
// create failed.
func (c cloudEnvironment) find(ctx context.Context, record allocationRecord) (string, error) {
	owner, spec, err := c.sandboxSpec(record)
	if err != nil {
		return "", err
	}
	return c.sandboxes.lookup(ctx, owner, spec)
}

func (c cloudEnvironment) resolve(ctx context.Context, record allocationRecord, client *http.Client) (environment, environmentState, error) {
	owner := Allocation{WorkID: record.SessionID, ID: record.ID}
	endpoint, ready, err := c.sandboxes.Endpoint(ctx, owner, record.SandboxID)
	if errors.Is(err, errSandboxAbsent) {
		return environment{}, environmentLost, nil
	}
	if err != nil || !ready {
		return environment{}, environmentPending, err
	}
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return environment{}, environmentPending, oops.With("operation", "decode_native_spec").Wrap(err)
	}
	execdClient, err := execd.NewClient(client, endpoint, spec.Env["EXECD_ACCESS_TOKEN"])
	if err != nil {
		return environment{}, environmentPending, err
	}
	return environment{endpoint: endpoint, token: spec.Env["EXECD_ACCESS_TOKEN"], spec: spec, client: execdClient}, environmentRunning, nil
}

func (c cloudEnvironment) lost(ctx context.Context, record allocationRecord) (bool, error) {
	_, err := c.sandboxes.client.GetSandbox(ctx, record.SandboxID)
	if isSandboxNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, sandboxError(ctx, "observe", err)
	}
	return false, nil
}

// release deletes the sandbox the obligation fenced.
func (c cloudEnvironment) release(ctx context.Context, _ allocationRecord, removal removalRecord) (bool, error) {
	return c.sandboxes.Delete(ctx, Allocation{WorkID: removal.SessionID, ID: removal.ID}, removal.SandboxID, c.credentials)
}

func (cloudEnvironment) workCredential(record allocationRecord) (string, error) {
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return "", oops.With("operation", "decode_allocation_spec").Wrap(err)
	}
	return spec.Env[workCredentialEnv], nil
}

func (c cloudEnvironment) deliver(ctx context.Context, record allocationRecord, redelivery bool) (deliveredCredentials, error) {
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return deliveredCredentials{}, oops.With("operation", "decode_native_request").Wrap(err)
	}
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return deliveredCredentials{}, oops.With("operation", "decode_native_spec").Wrap(err)
	}
	return c.credentials.prepare(ctx, record, admitted, spec, redelivery)
}

func (c cloudEnvironment) withdraw(ctx context.Context, record allocationRecord) error {
	return c.credentials.clear(ctx, record.SandboxID)
}

func (c cloudEnvironment) modelDigest(record allocationRecord) (string, error) {
	return c.credentials.modelDigest(record)
}

// prune preserves the durably selected primary sandbox.
func (c cloudEnvironment) prune(ctx context.Context, record allocationRecord, env environment) error {
	return c.sandboxes.prune(ctx, Allocation{WorkID: record.SessionID, ID: record.ID}, env.spec, record.SandboxID, c.credentials)
}

// pruneRetired removes late or duplicate sandboxes of a retired allocation. The
// allocation itself is never revived and its primary removal obligation is
// settled separately; it is consulted only when such a sandbox is observed.
func (c cloudEnvironment) pruneRetired(ctx context.Context, record allocationRecord) error {
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return oops.With("operation", "decode_retired_spec").Wrap(err)
	}
	return c.sandboxes.prune(ctx, Allocation{WorkID: record.SessionID, ID: record.ID}, spec, "", c.credentials)
}

// unreached reports that the allocation's Work reclaims its run beyond reach.
func (c cloudEnvironment) unreached(ctx context.Context, record allocationRecord) (bool, error) {
	if c.works == nil {
		return false, errors.New("agent: the Cloud realization has no Work holdings to authorize a release beyond reach")
	}
	return c.works.unreached(ctx, record)
}

// released observes a retired allocation's sandbox. Its absence can settle a
// previously observed exit, but cannot establish the exit of a new start that
// has not allocated its environment yet.
func (c cloudEnvironment) released(ctx context.Context, record allocationRecord) (bool, error) {
	info, err := c.sandboxes.client.GetSandbox(ctx, record.SandboxID)
	if isSandboxNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, sandboxError(ctx, "observe", err)
	}
	if info.ID != record.SandboxID || !matchesSandbox(info, Allocation{WorkID: record.SessionID, ID: record.ID}) {
		return false, errEnvironmentConflict
	}
	return false, nil
}
