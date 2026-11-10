package execution

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// CredentialManagementPort is the upstream egress API port.
const CredentialManagementPort = 18080

// SandboxCredentials owns native Vault I/O within the existing execution
// lifecycle. It has no independently persisted lifecycle or user grant API.
// The repository and service credentials it delivers are those Application
// supplied from Marketplace with each delivery; it calls no other authority.
type SandboxCredentials struct {
	db        *xorm.Engine
	lifecycle *sdk.LifecycleClient
	models    command.PlatformCatalogPort
	// records checks every delivery against its execution's facts.
	records   records
	http      *http.Client
	transport string
	now       func() time.Time
}

func NewSandboxCredentials(db *xorm.Engine, lifecycle *sdk.LifecycleClient, models command.PlatformCatalogPort, client *http.Client, transport string) (*SandboxCredentials, error) {
	if db == nil || lifecycle == nil || models == nil || client == nil {
		return nil, errors.New("agent: credential persistence, platform catalog and management client required")
	}
	// encrypted-network is enabled only by deployment that verifies its actual
	// cross-node encryption. It is not an option to fall back from failed TLS.
	if transport != "https" && transport != "encrypted-network" {
		return nil, errors.New("agent: encrypted Vault management transport required")
	}
	h := *client
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &SandboxCredentials{db: db, lifecycle: lifecycle, models: models, records: records{db: db}, http: &h, transport: transport}
	return c, nil
}

// withLock serializes preparation with reclamation across controlplane replicas.
// MySQL advisory locks belong to a physical connection, not a pooled session.
// Cleanup uses its own deadline so cancellation cannot return a locked connection.
func (c *SandboxCredentials) withLock(ctx context.Context, id string, work func() error) error {
	conn, err := c.db.DB().Conn(ctx)
	if err != nil {
		return errors.New("agent: credential reconciliation connection unavailable")
	}
	defer conn.Close()
	key := "vault:" + storageKey(id)[:48]
	var acquired int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", key).Scan(&acquired); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return errors.New("agent: credential reconciliation lock unavailable")
	}
	if acquired != 1 {
		return errEnvironmentConflict
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var released int
		if err := conn.QueryRowContext(release, "SELECT RELEASE_LOCK(?)", key).Scan(&released); err != nil || released != 1 {
			// sql.Conn.Raw's ErrBadConn retires the physical connection and its locks.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return work()
}

// errSupplyTemporarilyUnavailable means Marketplace could not supply a
// credential for now; it said nothing about the authorization itself.
var errSupplyTemporarilyUnavailable = errors.New("agent: credential supply temporarily unavailable")

// staleDelivery is the recorded expiry of a delivery that lacks material
// Marketplace could not supply for now: it is due at once, so the sweep fetches
// it again.
var staleDelivery = time.UnixMilli(1)

// prepare delivers the Work's current credentials, as Application supplied
// them, to the Vault: those Marketplace supplied are written, and the
// bindings of those it confirmed it cannot supply (revoked, reconnection
// required, refused) are removed while the Work keeps running. A temporary supply failure never removes a delivered
// binding: a redelivery fails to be retried, and a first delivery writes what
// it has and reports itself stale. It reports the service access observed and
// the earliest declared expiry of what it delivered, zero when nothing
// delivered expires.
func (c *SandboxCredentials) prepare(ctx context.Context, record executionRecord, admitted domain.LaunchSpecification, spec sdk.CreateSandboxRequest, supply command.CredentialSupply, redelivery bool, holding command.EnvironmentHolding) (deliveredCredentials, error) {
	statuses, expiry, digest, err := c.write(ctx, record, admitted, spec, supply, redelivery, holding)
	return deliveredCredentials{Statuses: statuses, ExpiresAt: expiry, ModelDigest: digest}, err
}

func (c *SandboxCredentials) write(ctx context.Context, record executionRecord, admitted domain.LaunchSpecification, spec sdk.CreateSandboxRequest, supply command.CredentialSupply, redelivery bool, holding command.EnvironmentHolding) ([]command.ServiceAccessStatus, time.Time, string, error) {
	var none time.Time
	if err := holding(ctx); err != nil {
		return nil, none, "", err
	}
	if spec.CredentialProxy == nil || !spec.CredentialProxy.Enabled || spec.NetworkPolicy == nil || spec.NetworkPolicy.DefaultAction != "allow" {
		return nil, none, "", errors.New("agent: credential configuration does not match admitted sandbox")
	}
	// A Model Configuration pointed at another template applies from the
	// Work's next start: this sandbox keeps the endpoint it was created with.
	// It receives the configuration's current key only while that key still
	// belongs to this endpoint; otherwise, as when the configuration is
	// withdrawn, it keeps the credential already delivered.
	model := renewalCredential(c.models, admitted.WorkModel, spec.Env["ANTHROPIC_BASE_URL"])
	var approved []RepositoryAuthorization
	if err := json.Unmarshal(record.CredentialPlanJSON, &approved); err != nil {
		return nil, none, "", errors.New("agent: invalid persisted credential plan")
	}
	for _, supplied := range supply.Repositories {
		// The plan admitted the repository and where it is reached; the
		// access identity only names its binding, which a delivery replaces.
		if !slices.ContainsFunc(approved, func(admitted RepositoryAuthorization) bool {
			return admitted.RepositoryID == supplied.RepositoryID && admitted.RepositoryHTTPSURL == supplied.CloneURL
		}) {
			return nil, none, "", errors.New("agent: current repository authorization differs from admitted credential plan")
		}
	}
	temporary := slices.ContainsFunc(supply.Access, func(s command.ServiceAccessStatus) bool { return s.Temporary })
	if temporary && redelivery {
		return nil, none, "", errSupplyTemporarilyUnavailable
	}
	now := c.clock()
	plan, err := credentialPlan(model, supply.Repositories, supply.Services, now)
	if err != nil {
		return nil, none, "", err
	}
	if plan, err = withPlatformCredentials(plan, spec.Env, supply.IdentityToken, supply.SpaceToken); err != nil {
		return nil, none, "", err
	}
	client, err := c.client(ctx, record.SandboxID, true)
	if err != nil {
		return nil, none, "", err
	}
	policy, err := client.GetPolicy(ctx)
	if err != nil {
		return nil, none, "", credentialError(ctx, "inspect_policy", err)
	}
	if policy.EnforcementMode != "dns+nft" || policy.Policy == nil || !reflect.DeepEqual(policy.Policy, spec.NetworkPolicy) {
		return nil, none, "", errors.New("agent: credential network policy not ready")
	}
	if err := holding(ctx); err != nil {
		return nil, none, "", err
	}
	before, err := client.GetCredentialVault(ctx)
	if err != nil && !isSandboxNotFound(err) {
		return nil, none, "", credentialError(ctx, "inspect_vault", err)
	}
	var revision int
	if isSandboxNotFound(err) {
		_, err = client.CreateCredentialVault(ctx, plan)
	} else if err == nil {
		revision = before.Revision
		patch := vaultReplacement(before, plan, retained(model))
		_, err = client.PatchCredentialVault(ctx, patch)
	}
	if err != nil && ctx.Err() != nil {
		return nil, none, "", ctx.Err()
	}
	// A lost write response is resolved by sanitized revision and exact binding
	// readback under the same exclusive lock. No second environment is allocated.
	after, readErr := client.GetCredentialVault(ctx)
	if readErr != nil {
		return nil, none, "", credentialError(ctx, "readback", readErr)
	}
	if !vaultMatches(after, plan, retained(model)) || after.Revision <= revision {
		if err != nil {
			// The write was refused, not lost: report why.
			return nil, none, "", credentialError(ctx, "write", err)
		}
		return nil, none, "", errors.New("agent: credential write not confirmed")
	}
	// An exit request admitted while writing must not leave the credentials
	// behind (Credential withdrawal).
	if err := holding(ctx); err != nil {
		if clearErr := c.clear(ctx, record.SandboxID); clearErr != nil {
			return nil, none, "", clearErr
		}
		return nil, none, "", err
	}
	if temporary {
		return supply.Access, staleDelivery, model.digest(), nil
	}
	return supply.Access, earliestExpiry(supply.Repositories, supply.Services), model.digest(), nil
}

func (c *SandboxCredentials) client(ctx context.Context, id string, ready bool) (*sdk.EgressClient, error) {
	proxy := false
	endpoint, err := c.lifecycle.GetEndpoint(ctx, id, CredentialManagementPort, &proxy)
	if err != nil {
		return nil, credentialError(ctx, "endpoint", err)
	}
	address := endpoint.Endpoint
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(c.transport == "encrypted-network" && u.Scheme == "http")) {
		return nil, errors.New("agent: credential management endpoint violates transport policy")
	}
	token := ""
	for k, v := range endpoint.Headers {
		if strings.EqualFold(k, "OPENSANDBOX-EGRESS-AUTH") {
			token = v
		}
	}
	if token == "" {
		return nil, errors.New("agent: authenticated egress endpoint required")
	}
	if ready {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(address, "/")+"/healthz", nil)
		if err != nil {
			return nil, errors.New("agent: invalid egress health endpoint")
		}
		for k, v := range endpoint.Headers {
			req.Header.Set(k, v)
		}
		response, err := c.http.Do(req)
		if err != nil {
			return nil, credentialError(ctx, "health", err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			return nil, errors.New("agent: egress not ready")
		}
	}
	return sdk.NewEgressClient(address, token, sdk.WithHTTPClient(c.http), sdk.WithHeaders(endpoint.Headers)), nil
}

func (c *SandboxCredentials) clear(ctx context.Context, id string) error {
	client, err := c.client(ctx, id, false)
	if err != nil {
		return err
	}
	// Preserve an empty Vault revision as a tombstone. A delayed initial POST
	// conflicts, and a delayed PATCH fails its old revision after cleanup.
	state, err := client.GetCredentialVault(ctx)
	empty := sdk.CredentialVaultCreateRequest{Credentials: []sdk.Credential{}, Bindings: []sdk.CredentialBinding{}}
	if isSandboxNotFound(err) {
		_, err = client.CreateCredentialVault(ctx, empty)
	} else if err == nil {
		_, err = client.PatchCredentialVault(ctx, vaultReplacement(state, empty, nil))
	}
	after, readErr := client.GetCredentialVault(ctx)
	if readErr != nil {
		return credentialError(ctx, "confirm_clear", readErr)
	}
	if !vaultMatches(after, empty, nil) {
		return errors.New("agent: credential clearing not confirmed")
	}
	return nil
}

// retained names the Vault entries a refresh keeps rather than replaces.
func retained(model modelCredential) []string {
	if model.Key == "" {
		return modelVaultEntries
	}
	return nil
}

func vaultReplacement(state *sdk.CredentialVaultState, plan sdk.CredentialVaultCreateRequest, keep []string) sdk.CredentialVaultPatchRequest {
	patch := sdk.CredentialVaultPatchRequest{ExpectedRevision: &state.Revision, Credentials: &sdk.CredentialMutationSet{}, Bindings: &sdk.CredentialBindingMutationSet{}}
	credentials := map[string]bool{}
	bindings := map[string]bool{}
	for _, v := range state.Credentials {
		credentials[v.Name] = true
	}
	for _, v := range state.Bindings {
		bindings[v.Name] = true
	}
	for _, v := range plan.Credentials {
		if credentials[v.Name] {
			patch.Credentials.Replace = append(patch.Credentials.Replace, v)
		} else {
			patch.Credentials.Add = append(patch.Credentials.Add, v)
		}
		delete(credentials, v.Name)
	}
	for _, v := range plan.Bindings {
		if bindings[v.Name] {
			patch.Bindings.Replace = append(patch.Bindings.Replace, v)
		} else {
			patch.Bindings.Add = append(patch.Bindings.Add, v)
		}
		delete(bindings, v.Name)
	}
	for name := range credentials {
		if !slices.Contains(keep, name) {
			patch.Credentials.Delete = append(patch.Credentials.Delete, name)
		}
	}
	for name := range bindings {
		if !slices.Contains(keep, name) {
			patch.Bindings.Delete = append(patch.Bindings.Delete, name)
		}
	}
	return patch
}
func vaultMatches(state *sdk.CredentialVaultState, plan sdk.CredentialVaultCreateRequest, keep []string) bool {
	if state == nil {
		return false
	}
	credentialsKept := len(slices.DeleteFunc(slices.Clone(state.Credentials), func(v sdk.CredentialMetadata) bool { return !slices.Contains(keep, v.Name) }))
	bindingsKept := len(slices.DeleteFunc(slices.Clone(state.Bindings), func(v sdk.CredentialBindingMetadata) bool { return !slices.Contains(keep, v.Name) }))
	if len(state.Credentials)-credentialsKept != len(plan.Credentials) || len(state.Bindings)-bindingsKept != len(plan.Bindings) {
		return false
	}
	credentials := map[string]bool{}
	for _, v := range state.Credentials {
		if v.SourceType != "inline" {
			return false
		}
		credentials[v.Name] = true
	}
	for _, v := range plan.Credentials {
		if !credentials[v.Name] {
			return false
		}
	}
	for _, want := range plan.Bindings {
		found := false
		for _, got := range state.Bindings {
			if got.Name == want.Name && got.Match != nil && got.Auth != nil && got.Auth.Type == string(want.Auth.Type) && reflect.DeepEqual(*got.Match, want.Match) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func credentialError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var api *sdk.APIError
	if errors.As(err, &api) {
		return fmt.Errorf("agent: credential %s returned HTTP %d", operation, api.StatusCode)
	}
	// HTTP errors and upstream payloads may echo material. Preserve only the
	// operation, never the cause or request body in logs/traces.
	return fmt.Errorf("agent: credential %s failed", operation)
}

func sortAuthorizations(values []RepositoryAuthorization) {
	slices.SortFunc(values, func(a, b RepositoryAuthorization) int {
		return strings.Compare(a.AccessID+"\x00"+a.RepositoryID+"\x00"+a.RepositoryHTTPSURL, b.AccessID+"\x00"+b.RepositoryID+"\x00"+b.RepositoryHTTPSURL)
	})
}

// claim freezes the access each repository the Work selected takes, as
// Application supplied it, with the specification: later deliveries supply
// credentials for these repositories only.
func claim(supply command.CreationSupply) ([]byte, error) {
	approved := make([]RepositoryAuthorization, 0, len(supply.Repositories))
	for _, reference := range supply.Repositories {
		approved = append(approved, RepositoryAuthorization{AccessID: reference.AccessID, RepositoryID: reference.RepositoryID, RepositoryHTTPSURL: reference.CloneURL})
	}
	sortAuthorizations(approved)
	encoded, err := json.Marshal(approved)
	if err != nil {
		return nil, errors.New("agent: invalid credential authorization plan")
	}
	return encoded, nil
}

// earliestExpiry is the soonest declared expiry of delivered material; zero
// when none of it expires.
func earliestExpiry(repositories []command.RepositoryAccessMaterial, services []command.ServiceAccessMaterial) time.Time {
	var earliest time.Time
	consider := func(expiresAt time.Time) {
		if !expiresAt.IsZero() && (earliest.IsZero() || expiresAt.Before(earliest)) {
			earliest = expiresAt
		}
	}
	for _, r := range repositories {
		consider(r.ExpiresAt)
	}
	for _, s := range services {
		consider(s.ExpiresAt)
	}
	return earliest
}

func (c *SandboxCredentials) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
