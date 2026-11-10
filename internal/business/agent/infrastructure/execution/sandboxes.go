package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/google/uuid"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	corev1 "k8s.io/api/core/v1"
)

const sandboxSessionLabel = "talgent.io/session"
const sandboxSpecLabel = "talgent.io/spec"

// sandboxOwnershipLabel names, by a digest of its identity, the AgentExecution
// a sandbox belongs to.
const sandboxOwnershipLabel = "talgent.io/execution"

// SandboxOwner identifies the execution of a Work whose sandboxes carry its
// ownership. It is an infrastructure identity for provider correlation only;
// a Work has at most one live execution.
type SandboxOwner struct {
	WorkID string
	ID     string
}

func (a SandboxOwner) validate() error {
	if strings.TrimSpace(a.WorkID) == "" || uuid.Validate(a.ID) != nil {
		return domain.ErrExecutionRequest
	}
	return nil
}

// Sandboxes performs external operations only. Callers record a creation's
// submission before submit and resolve an uncertain outcome by observing the
// sandboxes carrying the execution's ownership; submit is never retried
// merely because its response was lost.
type Sandboxes struct {
	client *sdk.LifecycleClient
	pods   *SandboxPods
	config SandboxConfig
}

func NewSandboxes(client *sdk.LifecycleClient, pods *SandboxPods, config SandboxConfig) (*Sandboxes, error) {
	if client == nil || pods == nil {
		return nil, errors.New("agent: lifecycle client and sandbox Pods are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	config.Environment = maps.Clone(config.Environment)
	config.NetworkDeny = slices.Clone(config.NetworkDeny)
	config.NetworkAllow = slices.Clone(config.NetworkAllow)
	config.Resources = *config.Resources.DeepCopy()
	return &Sandboxes{client: client, pods: pods, config: config}, nil
}

// errCreationTooLate means a recorded submission reached the sending only
// once too little of its creation wait remained for the server's own
// creation timeout: sent now, the creation could still produce a sandbox
// after its creation deadline, so it is not sent, and observation and the
// Startup Deadline decide the start.
var errCreationTooLate = errors.New("agent: the creation deadline leaves no room for the server's creation timeout")

// submit sends the create once and waits for its answer, which OpenSandbox
// gives once the sandbox is ready or has definitely failed, until the
// creation deadline the execution recorded before sending. It sends only
// while the server's creation timeout still ends before that deadline, so a
// creation can produce a sandbox only before it. A wait that ends first
// loses the answer.
func (s *Sandboxes) submit(ctx context.Context, owner SandboxOwner, spec sdk.CreateSandboxRequest, deadline time.Time) (string, error) {
	if time.Until(deadline) <= s.config.CreationTimeout {
		return "", errCreationTooLate
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, err := s.client.CreateSandbox(ctx, spec)
	if err != nil && ctx.Err() == nil && isCreationRefused(err) {
		return "", errors.Join(errCreationRefused, sandboxError(ctx, "create", err))
	}
	if err != nil {
		return "", sandboxError(ctx, "create", err)
	}
	if !carriesOwnership(result, owner, spec) || !validSandboxID(result.ID) {
		return "", errEnvironmentConflict
	}
	return result.ID, nil
}

// podSandbox is the environment a running Pod carrying the execution's
// ownership belongs to when the list did not show its sandbox: the sandbox
// itself while a GET still finds it owned by the execution, otherwise an
// ending environment, deleted while its Pod still runs. A sandbox found owned
// by another execution is ending too, so a doubtful Pod holds the release
// rather than allowing it.
func (s *Sandboxes) podSandbox(ctx context.Context, owner SandboxOwner, id string) (domain.ObservedEnvironment, error) {
	ending := domain.ObservedEnvironment{ID: id, Ending: true}
	if !validSandboxID(id) {
		return ending, nil
	}
	info, err := s.client.GetSandbox(ctx, id)
	if isSandboxNotFound(err) {
		return ending, nil
	}
	if err != nil {
		return domain.ObservedEnvironment{}, sandboxError(ctx, "inspect", err)
	}
	if !matchesSandbox(info, owner) || info.ID != id {
		return ending, nil
	}
	return domain.ObservedEnvironment{ID: id, Running: info.Status.State == sdk.StateRunning, LeaseExpiresAt: leaseExpiry(info)}, nil
}

// owned lists the sandboxes carrying the execution's exact ownership, each
// once. A listed sandbox that does not is not the execution's, so it is left
// in place without holding back the others.
func (s *Sandboxes) owned(ctx context.Context, owner SandboxOwner, spec sdk.CreateSandboxRequest) ([]sdk.SandboxInfo, error) {
	items, err := s.listCorrelated(ctx, owner)
	if err != nil {
		return nil, err
	}
	owned := make([]sdk.SandboxInfo, 0, len(items))
	for i := range items {
		item := &items[i]
		if carriesOwnership(item, owner, spec) && validSandboxID(item.ID) && !slices.ContainsFunc(owned, func(o sdk.SandboxInfo) bool { return o.ID == item.ID }) {
			owned = append(owned, *item)
		}
	}
	slices.SortFunc(owned, func(a, b sdk.SandboxInfo) int { return strings.Compare(a.ID, b.ID) })
	return owned, nil
}

// listCorrelated pages every sandbox the server lists with the execution's
// owner metadata.
func (s *Sandboxes) listCorrelated(ctx context.Context, owner SandboxOwner) ([]sdk.SandboxInfo, error) {
	filter := correlation(owner)
	var items []sdk.SandboxInfo
	for page := 1; ; page++ {
		result, err := s.client.ListSandboxes(ctx, sdk.ListOptions{Metadata: filter, Page: page, PageSize: 100})
		if err != nil {
			return nil, sandboxError(ctx, "lookup", err)
		}
		items = append(items, result.Items...)
		if !result.Pagination.HasNextPage {
			return items, nil
		}
		if len(result.Items) == 0 {
			return nil, errors.New("agent: inconsistent sandbox pagination")
		}
	}
}

// Only a GET of the bound sandbox can establish authoritative absence. An
// endpoint lookup's 404 is not equivalent to disappearance of that environment.
var errSandboxAbsent = errors.New("agent: bound sandbox is absent")

// boundSandbox is the bound sandbox as a GET shows it: its direct execd
// endpoint while it runs, and its lease's expiry, zero when it was created
// without one.
type boundSandbox struct {
	endpoint  string
	running   bool
	expiresAt time.Time
}

// inspect shows only a matching sandbox, resolving direct execd access while
// it runs. Gateway credentials are deliberately not interpreted as execd
// tokens.
func (s *Sandboxes) inspect(ctx context.Context, owner SandboxOwner, id string) (boundSandbox, error) {
	if err := validateSandboxOwner(owner, id); err != nil {
		return boundSandbox{}, err
	}
	info, err := s.client.GetSandbox(ctx, id)
	if isSandboxNotFound(err) {
		return boundSandbox{}, errSandboxAbsent
	}
	if err != nil {
		return boundSandbox{}, sandboxError(ctx, "inspect", err)
	}
	if !matchesSandbox(info, owner) || info.ID != id {
		return boundSandbox{}, errEnvironmentConflict
	}
	bound := boundSandbox{expiresAt: leaseExpiry(info)}
	if info.Status.State != sdk.StateRunning {
		return bound, nil
	}
	proxy := false
	endpoint, err := s.client.GetEndpoint(ctx, id, 44772, &proxy)
	if err != nil {
		return boundSandbox{}, sandboxError(ctx, "endpoint", err)
	}
	if len(endpoint.Headers) != 0 {
		return boundSandbox{}, errors.New("agent: direct execd endpoint must not require gateway headers")
	}
	address := endpoint.Endpoint
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return boundSandbox{}, errors.New("agent: invalid execd endpoint")
	}
	bound.endpoint, bound.running = address, true
	return bound, nil
}

// leaseExpiry is when OpenSandbox deletes the sandbox unless renewed; zero
// for a sandbox created without an expiry.
func leaseExpiry(info *sdk.SandboxInfo) time.Time {
	if info.ExpiresAt == nil {
		return time.Time{}
	}
	return recordedExpiry(*info.ExpiresAt)
}

// recordedExpiry is an expiry OpenSandbox returned, to the millisecond the
// execution's record keeps, so observing a recorded lease again changes
// nothing.
func recordedExpiry(expiresAt time.Time) time.Time {
	return expiresAt.Truncate(time.Millisecond).UTC()
}

// renew moves the bound sandbox's expiry to expiresAt and returns the expiry
// OpenSandbox answered. OpenSandbox accepts any future time, a shorter one
// included, so the caller asks only for later ones; it refuses a sandbox
// created without an expiry.
func (s *Sandboxes) renew(ctx context.Context, owner SandboxOwner, id string, expiresAt time.Time) (time.Time, error) {
	if err := validateSandboxOwner(owner, id); err != nil {
		return time.Time{}, err
	}
	answer, err := s.client.RenewExpiration(ctx, id, expiresAt)
	if isSandboxNotFound(err) {
		return time.Time{}, errSandboxAbsent
	}
	if err != nil {
		return time.Time{}, sandboxError(ctx, "renew", err)
	}
	return recordedExpiry(answer.ExpiresAt), nil
}

// Delete removes one sandbox once its exact ownership is confirmed, and
// reports whether it is already absent; only OpenSandbox's observation
// establishes that. Underlying Pod termination belongs to Kubernetes. No PVCs
// or contents are deleted here.
func (s *Sandboxes) Delete(ctx context.Context, owner SandboxOwner, spec sdk.CreateSandboxRequest, id string, credentials *SandboxCredentials) (bool, error) {
	if err := validateSandboxOwner(owner, id); err != nil {
		return false, err
	}
	info, err := s.client.GetSandbox(ctx, id)
	if isSandboxNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, sandboxError(ctx, "inspect_before_delete", err)
	}
	if info.ID != id || !carriesOwnership(info, owner, spec) {
		return false, errEnvironmentConflict
	}
	if credentials == nil {
		return false, errors.New("agent: credential cleanup is required")
	}
	var absent bool
	err = credentials.withLock(ctx, id, func() error {
		var err error
		absent, err = s.deleteLocked(ctx, owner, id, credentials)
		return err
	})
	return absent, err
}

func (s *Sandboxes) deleteLocked(ctx context.Context, owner SandboxOwner, id string, credentials *SandboxCredentials) (bool, error) {
	if err := validateSandboxOwner(owner, id); err != nil {
		return false, err
	}
	info, err := s.client.GetSandbox(ctx, id)
	if isSandboxNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, sandboxError(ctx, "inspect_before_delete", err)
	}
	if info.ID != id || !matchesSandbox(info, owner) {
		return false, errEnvironmentConflict
	}
	clearCtx, cancel := context.WithTimeout(ctx, time.Second)
	clearErr := credentials.clear(clearCtx, id)
	cancel()
	// A broken egress process must not prevent deleting its owned environment.
	// Keep the bounded cleanup failure diagnostic while deletion advances.
	if err := s.client.DeleteSandbox(ctx, id); err != nil && !isSandboxNotFound(err) {
		return false, errors.Join(clearErr, sandboxError(ctx, "delete", err))
	}
	return false, clearErr
}

func (s *Sandboxes) specification(ctx context.Context, request domain.LaunchSpecification, mounts domain.SandboxMounts, owner SandboxOwner, token string) (sdk.CreateSandboxRequest, error) {
	if err := validateStartRequest(request); err != nil {
		return sdk.CreateSandboxRequest{}, err
	}
	if err := owner.validate(); err != nil || owner.WorkID != request.WorkID {
		return sdk.CreateSandboxRequest{}, domain.ErrExecutionRequest
	}
	if strings.TrimSpace(token) == "" {
		return sdk.CreateSandboxRequest{}, domain.ErrExecutionRequest
	}
	model, provided := s.config.Models.ProvidedModel(request.WorkModel.ConfigurationID)
	if !provided {
		return sdk.CreateSandboxRequest{}, errModelConfigurationNotProvided
	}
	env := maps.Clone(s.config.Environment)
	if env == nil {
		env = make(map[string]string)
	}
	modelEnvironment(env, model.Template)
	attributes, err := telemetryAttribution(request)
	if err != nil {
		return sdk.CreateSandboxRequest{}, err
	}
	// The sandbox reaches the in-cluster Collector the configured
	// environment names, which takes its telemetry as sent.
	telemetryEnvironment(ctx, env, attributes)
	env["ANTHROPIC_AUTH_TOKEN"] = "opensandbox-credential-proxy"
	// Native clients require a nonempty token to attempt an authenticated request.
	// These public placeholders convey no authority; the egress Vault injects
	// current material only for the Work's selected, supported target contracts.
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"} {
		env[name] = "opensandbox-credential-proxy"
	}
	env["GH_PROMPT_DISABLED"] = "1"
	env["NODE_EXTRA_CA_CERTS"] = "/opt/opensandbox/mitmproxy-ca-cert.pem"
	for _, key := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
		env[key] = "/opt/opensandbox/merged-ca-certificates.pem"
	}
	harness, err := domain.AgentHarnessByID(request.AgentHarness)
	if err != nil {
		return sdk.CreateSandboxRequest{}, errors.Join(domain.ErrExecutionRequest, err)
	}
	// The Harness keeps its state in its Harness state mount, shared by the
	// Works that locate the same Directory; its variable points it there.
	env["HOME"] = homeMountPath
	env[harness.StateVariable] = domain.SandboxPath(harness.StateMountPath(), homeMountPath)
	env["EXECD_ACCESS_TOKEN"] = token
	env["TALGENT_WORK_ID"] = request.WorkID
	// The Work root, as a Managed Computer names it too, so the runtime
	// plugin and preparation read the root from one variable everywhere.
	env["TALGENT_WORK_ROOT"] = "/workspace"
	volumes, err := sandboxVolumes(request, mounts)
	if err != nil {
		return sdk.CreateSandboxRequest{}, err
	}
	// Every sandbox expires unless its execution renews it, so one Agent
	// lost track of is reclaimed within a lease.
	lease := int(s.config.Lease.Duration / time.Second)
	spec := sdk.CreateSandboxRequest{
		Image: &sdk.ImageSpec{URI: s.config.Image}, Timeout: &lease,
		Entrypoint: []string{"sleep", "infinity"}, Env: env, Metadata: correlation(owner),
		ResourceLimits: sdk.ResourceLimits{}, ResourceRequests: sdk.ResourceLimits{},
		NetworkPolicy:   &sdk.NetworkPolicy{DefaultAction: "allow"},
		CredentialProxy: &sdk.CredentialProxyConfig{Enabled: true},
		Volumes:         volumes,
	}
	policy, err := s.networkPolicy(ctx)
	if err != nil {
		return sdk.CreateSandboxRequest{}, err
	}
	spec.NetworkPolicy = policy
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		request, limit := s.config.Resources.Requests[name], s.config.Resources.Limits[name]
		spec.ResourceRequests[string(name)], spec.ResourceLimits[string(name)] = request.String(), limit.String()
	}
	encoded, err := json.Marshal(struct {
		Request      domain.LaunchSpecification
		SandboxOwner SandboxOwner
		Spec         sdk.CreateSandboxRequest
	}{request, owner, spec})
	if err != nil {
		return sdk.CreateSandboxRequest{}, oops.Wrap(err)
	}
	spec.Metadata[sandboxSpecLabel] = storageKey(string(encoded))[:48]
	return spec, nil
}

func correlation(owner SandboxOwner) map[string]string {
	return map[string]string{sandboxSessionLabel: storageKey(owner.WorkID)[:48], sandboxOwnershipLabel: storageKey(owner.ID)[:48]}
}

// matchesSandbox reports a sandbox carrying the execution's owner metadata.
func matchesSandbox(info *sdk.SandboxInfo, owner SandboxOwner) bool {
	if info == nil {
		return false
	}
	wanted := correlation(owner)
	return info.Metadata[sandboxSessionLabel] == wanted[sandboxSessionLabel] && info.Metadata[sandboxOwnershipLabel] == wanted[sandboxOwnershipLabel]
}

// carriesOwnership reports a sandbox carrying the execution's owner metadata
// and its immutable specification.
func carriesOwnership(info *sdk.SandboxInfo, owner SandboxOwner, spec sdk.CreateSandboxRequest) bool {
	return matchesSandbox(info, owner) && info.Metadata[sandboxSpecLabel] == spec.Metadata[sandboxSpecLabel]
}

func validSandboxID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
func validateSandboxOwner(owner SandboxOwner, id string) error {
	if err := owner.validate(); err != nil {
		return err
	}
	if !validSandboxID(id) {
		return domain.ErrExecutionRequest
	}
	return nil
}

// errCreationRefused marks a create the provider definitely refused or
// failed. Nothing it created for it remains, so its execution can never
// hold compute.
var errCreationRefused = errors.New("agent: sandbox creation refused")

// definiteCreationFailures are OpenSandbox's codes for a create that failed
// after the server created the sandbox, which it deletes before answering:
// its readiness timeout, a failed Pod and an exhausted quota.
var definiteCreationFailures = []string{"KUBERNETES::POD_READY_TIMEOUT", "KUBERNETES::POD_FAILED", "KUBERNETES::QUOTA_EXCEEDED"}

// isCreationRefused recognizes OpenSandbox's definite answers to a create:
// a refused specification or an unschedulable sandbox, and a failure after
// which the server deleted the sandbox. Any other failure, such as an
// unavailable server, leaves the outcome unknown.
func isCreationRefused(err error) bool {
	var api *sdk.APIError
	if !errors.As(err, &api) {
		return false
	}
	return api.StatusCode == http.StatusBadRequest || api.StatusCode == http.StatusUnprocessableEntity || slices.Contains(definiteCreationFailures, api.Response.Code)
}

func isSandboxNotFound(err error) bool {
	var api *sdk.APIError
	return errors.As(err, &api) && api.StatusCode == http.StatusNotFound
}
func sandboxError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var api *sdk.APIError
	if errors.As(err, &api) {
		// API response bodies can echo environment credentials and launch input.
		err = fmt.Errorf("OpenSandbox returned HTTP %d", api.StatusCode)
	}
	return oops.With("operation", operation).Wrap(err)
}
