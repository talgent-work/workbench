package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// cloudEnvironment is the Cloud realization's environment authority: an
// OpenSandbox sandbox per execution, created from its fixed specification,
// with the Work's credentials delivered to the sandbox's Vault. A creation
// that cannot be confirmed may leave more than one sandbox carrying the
// execution's ownership; observation lists them all, and the execution
// decides which to delete.
type cloudEnvironment struct {
	sandboxes   *Sandboxes
	credentials *SandboxCredentials
}

var (
	_ environmentAuthority = cloudEnvironment{}
	_ credentialDelivery   = cloudEnvironment{}
	_ leasing              = cloudEnvironment{}
)

func (cloudEnvironment) launchLayout() launchLayout { return sandboxLayout }

func (c cloudEnvironment) specify(ctx context.Context, request domain.LaunchSpecification, mounts domain.SandboxMounts, owner SandboxOwner, token, credential string) (environmentSpecification, error) {
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

// sandboxSpec is the execution's validated sandbox specification.
func (c cloudEnvironment) sandboxSpec(record executionRecord) (SandboxOwner, sdk.CreateSandboxRequest, error) {
	owner, spec, err := c.ownership(record)
	if err != nil {
		return owner, spec, err
	}
	if err := owner.validate(); err != nil {
		return owner, spec, err
	}
	if spec.Env["EXECD_ACCESS_TOKEN"] == "" {
		return owner, spec, domain.ErrExecutionRequest
	}
	return owner, spec, nil
}

// ownership is what the execution's sandboxes carry: its owner and the
// specification fixed before its submission, whose metadata the server lists
// by.
func (cloudEnvironment) ownership(record executionRecord) (SandboxOwner, sdk.CreateSandboxRequest, error) {
	owner := SandboxOwner{WorkID: record.SessionID, ID: record.ID}
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return owner, spec, oops.With("operation", "decode_execution_spec").Wrap(err)
	}
	if spec.Metadata[sandboxSpecLabel] == "" {
		return owner, spec, domain.ErrExecutionRequest
	}
	return owner, spec, nil
}

// claim freezes the Work's repository authorization with the specification,
// before the creation is submitted.
func (c cloudEnvironment) claim(ctx context.Context, record executionRecord) ([]byte, error) {
	if _, _, err := c.sandboxSpec(record); err != nil {
		return nil, err
	}
	request, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return nil, err
	}
	return c.credentials.plan(ctx, request.HomeBinding.OwnerID, record.SessionID)
}

func (c cloudEnvironment) create(ctx context.Context, record executionRecord) (string, error) {
	owner, spec, err := c.sandboxSpec(record)
	if err != nil {
		return "", err
	}
	return c.sandboxes.submit(ctx, owner, spec)
}

// observe lists the sandboxes the server shows with the execution's
// ownership. The list may not show a sandbox yet, so the bound one is
// confirmed by its own identity: present exactly while a GET finds it. A Pod
// carrying the execution's ownership whose sandbox is no longer shown is an
// ending environment: deleted, yet still running (Ending environment).
func (c cloudEnvironment) observe(ctx context.Context, record executionRecord, _ bool) (environmentView, error) {
	owner, spec, err := c.ownership(record)
	if err != nil {
		return environmentView{}, err
	}
	owned, err := c.sandboxes.owned(ctx, owner, spec)
	if err != nil {
		return environmentView{}, err
	}
	var view environmentView
	for _, info := range owned {
		if info.ID != record.SandboxID {
			view.environments = append(view.environments, domain.ObservedEnvironment{ID: info.ID, Running: info.Status.State == sdk.StateRunning, LeaseExpiresAt: leaseExpiry(&info)})
		}
	}
	if record.SandboxID != "" {
		bound, err := c.sandboxes.inspect(ctx, owner, record.SandboxID)
		switch {
		case errors.Is(err, errSandboxAbsent):
		case err != nil:
			return environmentView{}, err
		default:
			view.environments = append([]domain.ObservedEnvironment{{ID: record.SandboxID, Running: bound.running, LeaseExpiresAt: bound.expiresAt}}, view.environments...)
			view.endpoint = bound.endpoint
		}
	}
	running, err := c.sandboxes.pods.running(ctx, owner)
	if err != nil {
		return environmentView{}, err
	}
	for _, id := range running {
		if !slices.ContainsFunc(view.environments, func(e domain.ObservedEnvironment) bool { return e.ID == id }) {
			view.environments = append(view.environments, domain.ObservedEnvironment{ID: id, Ending: true})
		}
	}
	return view, nil
}

// renew extends the bound sandbox's lease to expiresAt.
func (c cloudEnvironment) renew(ctx context.Context, record executionRecord, expiresAt time.Time) (time.Time, error) {
	return c.sandboxes.renew(ctx, SandboxOwner{WorkID: record.SessionID, ID: record.ID}, record.SandboxID, expiresAt)
}

// remove deletes one sandbox once its exact ownership is confirmed again.
func (c cloudEnvironment) remove(ctx context.Context, record executionRecord, id string) error {
	owner, spec, err := c.ownership(record)
	if err != nil {
		return err
	}
	_, err = c.sandboxes.Delete(ctx, owner, spec, id, c.credentials)
	return err
}

func (c cloudEnvironment) resolve(ctx context.Context, record executionRecord, client *http.Client) (environment, environmentState, error) {
	owner := SandboxOwner{WorkID: record.SessionID, ID: record.ID}
	bound, err := c.sandboxes.inspect(ctx, owner, record.SandboxID)
	if errors.Is(err, errSandboxAbsent) {
		return environment{}, environmentLost, nil
	}
	if err != nil || !bound.running {
		return environment{}, environmentPending, err
	}
	endpoint := bound.endpoint
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return environment{}, environmentPending, oops.With("operation", "decode_native_spec").Wrap(err)
	}
	execdClient, err := execd.NewClient(client, endpoint, spec.Env["EXECD_ACCESS_TOKEN"])
	if err != nil {
		return environment{}, environmentPending, err
	}
	return environment{endpoint: endpoint, token: spec.Env["EXECD_ACCESS_TOKEN"], client: execdClient}, environmentRunning, nil
}

func (cloudEnvironment) workCredential(record executionRecord) (string, error) {
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return "", oops.With("operation", "decode_execution_spec").Wrap(err)
	}
	return spec.Env[workCredentialEnv], nil
}

func (c cloudEnvironment) deliver(ctx context.Context, record executionRecord, redelivery bool, holding command.EnvironmentHolding) (deliveredCredentials, error) {
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return deliveredCredentials{}, oops.With("operation", "decode_native_request").Wrap(err)
	}
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return deliveredCredentials{}, oops.With("operation", "decode_native_spec").Wrap(err)
	}
	return c.credentials.prepare(ctx, record, admitted, spec, redelivery, holding)
}

func (c cloudEnvironment) withdraw(ctx context.Context, record executionRecord) error {
	return c.credentials.clear(ctx, record.SandboxID)
}

func (c cloudEnvironment) modelDigest(record executionRecord) (string, error) {
	return c.credentials.modelDigest(record)
}
