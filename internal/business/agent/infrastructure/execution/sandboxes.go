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
const sandboxAllocationLabel = "talgent.io/allocation"
const sandboxSpecLabel = "talgent.io/spec"

// workCredentialEnv names, in the recorded sandbox specification, the native
// process credential issued to one sandbox. It authorizes only while that
// sandbox is its Work's live sandbox. It is kept out of the sandbox itself:
// the Credential Proxy injects it into the requests to the platform.
const workCredentialEnv = "TALGENT_WORK_CREDENTIAL"

// Allocation identifies one sandbox allocation for a Work. It is an
// infrastructure identity for provider correlation only; a Work has at most one
// live allocation, and no allocation identity leaves execution infrastructure.
type Allocation struct {
	WorkID string
	ID     string
}

func (a Allocation) validate() error {
	if strings.TrimSpace(a.WorkID) == "" || uuid.Validate(a.ID) != nil {
		return domain.ErrExecutionRequest
	}
	return nil
}

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
}

func (c SandboxConfig) Validate() error {
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

// Sandboxes performs external operations only. Callers record a creation's
// submission before submit and resolve an uncertain outcome by observing the
// sandboxes carrying the execution's ownership; submit is never retried
// merely because its response was lost.
type Sandboxes struct {
	client *sdk.LifecycleClient
	config SandboxConfig
}

func NewSandboxes(client *sdk.LifecycleClient, config SandboxConfig) (*Sandboxes, error) {
	if client == nil {
		return nil, errors.New("agent: lifecycle client is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	config.Environment = maps.Clone(config.Environment)
	config.NetworkDeny = slices.Clone(config.NetworkDeny)
	config.NetworkAllow = slices.Clone(config.NetworkAllow)
	config.Resources = *config.Resources.DeepCopy()
	return &Sandboxes{client: client, config: config}, nil
}

func (s *Sandboxes) submit(ctx context.Context, owner Allocation, spec sdk.CreateSandboxRequest) (string, error) {
	result, err := s.client.CreateSandbox(ctx, withoutWorkCredential(spec))
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

// withoutWorkCredential is the specification as the sandbox receives it: the
// Work credential stays with the platform and reaches it only
// through the Credential Proxy.
func withoutWorkCredential(spec sdk.CreateSandboxRequest) sdk.CreateSandboxRequest {
	spec.Env = maps.Clone(spec.Env)
	delete(spec.Env, workCredentialEnv)
	return spec
}

// owned lists the sandboxes carrying the execution's exact ownership, each
// once. A listed sandbox that does not is not the execution's, so it is left
// in place without holding back the others.
func (s *Sandboxes) owned(ctx context.Context, owner Allocation, spec sdk.CreateSandboxRequest) ([]sdk.SandboxInfo, error) {
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

// listCorrelated pages every sandbox the server lists with the allocation's
// owner metadata.
func (s *Sandboxes) listCorrelated(ctx context.Context, owner Allocation) ([]sdk.SandboxInfo, error) {
	var items []sdk.SandboxInfo
	for page := 1; ; page++ {
		result, err := s.client.ListSandboxes(ctx, sdk.ListOptions{Metadata: correlation(owner), Page: page, PageSize: 100})
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

// Endpoint resolves only a running, matching sandbox and returns direct execd
// access. Gateway credentials are deliberately not interpreted as execd tokens.
func (s *Sandboxes) Endpoint(ctx context.Context, owner Allocation, id string) (string, bool, error) {
	if err := validateSandboxOwner(owner, id); err != nil {
		return "", false, err
	}
	info, err := s.client.GetSandbox(ctx, id)
	if isSandboxNotFound(err) {
		return "", false, errSandboxAbsent
	}
	if err != nil {
		return "", false, sandboxError(ctx, "inspect", err)
	}
	if !matchesSandbox(info, owner) || info.ID != id {
		return "", false, errEnvironmentConflict
	}
	if info.Status.State != sdk.StateRunning {
		return "", false, nil
	}
	proxy := false
	endpoint, err := s.client.GetEndpoint(ctx, id, 44772, &proxy)
	if err != nil {
		return "", false, sandboxError(ctx, "endpoint", err)
	}
	if len(endpoint.Headers) != 0 {
		return "", false, errors.New("agent: direct execd endpoint must not require gateway headers")
	}
	address := endpoint.Endpoint
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false, errors.New("agent: invalid execd endpoint")
	}
	return address, true, nil
}

// Delete removes one sandbox once its exact ownership is confirmed, and
// reports whether it is already absent; only OpenSandbox's observation
// establishes that. Underlying Pod termination belongs to Kubernetes. No PVCs
// or contents are deleted here.
func (s *Sandboxes) Delete(ctx context.Context, owner Allocation, spec sdk.CreateSandboxRequest, id string, credentials *SandboxCredentials) (bool, error) {
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

func (s *Sandboxes) deleteLocked(ctx context.Context, owner Allocation, id string, credentials *SandboxCredentials) (bool, error) {
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

func (s *Sandboxes) specification(ctx context.Context, request domain.LaunchSpecification, mounts domain.SandboxMounts, owner Allocation, token, credential string) (sdk.CreateSandboxRequest, error) {
	if err := validateStartRequest(request); err != nil {
		return sdk.CreateSandboxRequest{}, err
	}
	if err := owner.validate(); err != nil || owner.WorkID != request.WorkID {
		return sdk.CreateSandboxRequest{}, domain.ErrExecutionRequest
	}
	if strings.TrimSpace(token) == "" || strings.TrimSpace(credential) == "" {
		return sdk.CreateSandboxRequest{}, domain.ErrExecutionRequest
	}
	model, provided := s.config.Models.Resolve(request.WorkModel.ConfigurationID)
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
	env["HOME"], env["CLAUDE_CONFIG_DIR"] = "/home/talgent", "/home/talgent/.claude"
	env["CLAUDE_CODE_PROJECT_DIR_NAME"] = storageKey(request.HomeBinding.Namespace)
	env["EXECD_ACCESS_TOKEN"] = token
	env["TALGENT_WORK_ID"] = request.WorkID
	// The Work root, as a Managed Computer names it too, so the runtime
	// plugin and preparation read the root from one variable everywhere.
	env["TALGENT_WORK_ROOT"] = "/workspace"
	env[workCredentialEnv] = credential
	volumes, err := sandboxVolumes(request, mounts)
	if err != nil {
		return sdk.CreateSandboxRequest{}, err
	}
	spec := sdk.CreateSandboxRequest{
		Image: &sdk.ImageSpec{URI: s.config.Image},
		// nil timeout deliberately omits automatic expiration. Session owns stop.
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
		Request    domain.LaunchSpecification
		Allocation Allocation
		Spec       sdk.CreateSandboxRequest
	}{request, owner, spec})
	if err != nil {
		return sdk.CreateSandboxRequest{}, oops.Wrap(err)
	}
	spec.Metadata[sandboxSpecLabel] = storageKey(string(encoded))[:48]
	return spec, nil
}

func correlation(owner Allocation) map[string]string {
	return map[string]string{sandboxSessionLabel: storageKey(owner.WorkID)[:48], sandboxAllocationLabel: storageKey(owner.ID)[:48]}
}

func matchesSandbox(info *sdk.SandboxInfo, owner Allocation) bool {
	if info == nil {
		return false
	}
	for key, value := range correlation(owner) {
		if info.Metadata[key] != value {
			return false
		}
	}
	return true
}

// carriesOwnership reports a sandbox carrying the allocation's owner metadata
// and its immutable specification.
func carriesOwnership(info *sdk.SandboxInfo, owner Allocation, spec sdk.CreateSandboxRequest) bool {
	return matchesSandbox(info, owner) && info.Metadata[sandboxSpecLabel] == spec.Metadata[sandboxSpecLabel]
}

func validSandboxID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
func validateSandboxOwner(owner Allocation, id string) error {
	if err := owner.validate(); err != nil {
		return err
	}
	if !validSandboxID(id) {
		return domain.ErrExecutionRequest
	}
	return nil
}

// errCreationRefused marks a create the provider answered by refusing its
// specification. The provider created nothing for it, so its allocation can
// never hold compute.
var errCreationRefused = errors.New("agent: sandbox creation refused")

// isCreationRefused recognizes OpenSandbox's answer to an invalid sandbox
// specification, which it rejects before creating any workload.
func isCreationRefused(err error) bool {
	var api *sdk.APIError
	return errors.As(err, &api) && (api.StatusCode == http.StatusBadRequest || api.StatusCode == http.StatusUnprocessableEntity)
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
