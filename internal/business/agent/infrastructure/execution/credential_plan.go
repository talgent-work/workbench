package execution

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type RepositoryAuthorization struct {
	AccessID           string `json:"grantId"`
	RepositoryID       string `json:"repositoryId"`
	RepositoryHTTPSURL string `json:"repositoryHttpsUrl"`
}

// RepositoryCredential contains material from Marketplace's current authorization
// authority. It is transient and must never be persisted by Agent infrastructure.
type RepositoryCredential struct {
	AccessID, RepositoryID, RepositoryHTTPSURL, Username, Password string
	ExpiresAt                                                      time.Time
}

func credentialURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(u.Path, "*?%\\") || (u.Path != "" && u.Path != "/" && path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/")) {
		return nil, errors.New("agent: credential target must be a canonical HTTPS URL on port 443")
	}
	return u, nil
}

// modelCredential is the Work's Model Configuration key for the endpoint its
// sandbox was created with. An empty Key keeps the credential already
// delivered: a withdrawn configuration leaves a running Work unaffected.
type modelCredential struct{ Endpoint, Key string }

// digest identifies the key without revealing it, so a rotated key changes the
// desired Vault state while the sandbox specification stays put.
func (m modelCredential) digest() string {
	if m.Key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(m.Key))
	return hex.EncodeToString(sum[:16])
}

// modelDigest identifies the model key a delivery to the execution carries
// now under the current Model Configurations, empty when it carries none. A
// configuration's key is rotated by redeploying it, so a recorded digest that
// differs marks a delivery due.
func (c *SandboxCredentials) modelDigest(record executionRecord) (string, error) {
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return "", err
	}
	var spec struct{ Env map[string]string }
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return "", err
	}
	return renewalCredential(c.models, admitted.WorkModel, spec.Env["ANTHROPIC_BASE_URL"]).digest(), nil
}

// renewalCredential is the model credential a running sandbox receives on its
// created endpoint: the configuration's current key while it still belongs to
// that endpoint, otherwise none, keeping what was delivered.
func renewalCredential(models *ModelCatalog, selection domain.WorkModelSelection, endpoint string) modelCredential {
	model := modelCredential{Endpoint: endpoint}
	if provided, ok := models.Resolve(selection.ConfigurationID); ok && provided.Template.BaseURL == endpoint {
		model.Key = provided.Configuration.APIKey
	}
	return model
}

// The Vault entries that carry the model credential.
var modelVaultEntries = []string{"model", "model-messages"}

// credentialPlan supplies only the Work's Model Configuration key, bound to the
// sandbox's model endpoint, beside the Work's repository and service access.
func credentialPlan(model modelCredential, repositories []RepositoryCredential, services []application.ServiceAccessMaterial, now time.Time) (sdk.CredentialVaultCreateRequest, error) {
	u, err := credentialURL(model.Endpoint)
	if err != nil {
		return sdk.CredentialVaultCreateRequest{}, err
	}
	result := sdk.CredentialVaultCreateRequest{}
	bind := func(name, host, method string, paths []string, credential string, auth sdk.CredentialAuthType) {
		result.Bindings = append(result.Bindings, sdk.CredentialBinding{Name: name, Match: sdk.CredentialMatch{Schemes: []sdk.CredentialScheme{sdk.CredentialSchemeHTTPS}, Hosts: []string{strings.ToLower(host)}, Methods: []string{method}, Paths: paths}, Auth: sdk.CredentialAuth{Type: auth, Credential: credential}})
	}
	if model.Key != "" {
		result.Credentials = append(result.Credentials, sdk.Credential{Name: "model", Source: sdk.InlineCredentialSource{Value: model.Key}})
		prefix := strings.TrimSuffix(u.Path, "/")
		bind("model-messages", u.Hostname(), "POST", []string{prefix + "/v1/messages", prefix + "/v1/messages/count_tokens"}, "model", sdk.CredentialAuthBearer)
	}
	seen := map[string]bool{}
	for _, r := range repositories {
		u, err := credentialURL(r.RepositoryHTTPSURL)
		if err != nil {
			return sdk.CredentialVaultCreateRequest{}, err
		}
		if r.AccessID == "" || r.RepositoryID == "" || r.Username == "" || r.Password == "" || expired(r.ExpiresAt, now) || strings.Contains(r.Username, ":") || u.Path == "" {
			return sdk.CredentialVaultCreateRequest{}, errors.New("agent: active repository credential required")
		}
		repo := strings.TrimSuffix(u.Path, "/")
		key := storageKey(u.Hostname() + repo)[:32]
		if seen[key] {
			return sdk.CredentialVaultCreateRequest{}, errors.New("agent: duplicate repository credential target")
		}
		seen[key] = true
		name := "git-" + key
		// Native basic auth prepends Basic; transient material is encoded here.
		result.Credentials = append(result.Credentials, sdk.Credential{Name: name, Source: sdk.InlineCredentialSource{Value: base64.StdEncoding.EncodeToString([]byte(r.Username + ":" + r.Password))}})
		bind(name+"-discovery", u.Hostname(), "GET", []string{repo + "/info/refs"}, name, sdk.CredentialAuthBasic)
		bind(name+"-transfer", u.Hostname(), "POST", []string{repo + "/git-upload-pack", repo + "/git-receive-pack", repo + "/info/lfs/objects/batch"}, name, sdk.CredentialAuthBasic)
	}
	for _, service := range services {
		if service.Service != "github" || service.Provider != "github" || service.AuthorizationID == "" || service.Token == "" || expired(service.ExpiresAt, now) {
			return sdk.CredentialVaultCreateRequest{}, errors.New("agent: current supported service credential required")
		}
		name := service.Target
		if seen[name] {
			return sdk.CredentialVaultCreateRequest{}, errors.New("agent: duplicate service credential target")
		}
		seen[name] = true
		switch service.Target {
		case "github-api":
			// Match the official Vault's explicit default methods and path glob.
			// Omitted lists are expanded on write, which would otherwise fail our
			// exact binding readback before the Work can start.
			result.Bindings = append(result.Bindings, sdk.CredentialBinding{Name: name,
				Match: sdk.CredentialMatch{Schemes: []sdk.CredentialScheme{sdk.CredentialSchemeHTTPS}, Hosts: []string{"api.github.com"}, Methods: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}, Paths: []string{"/*"}},
				Auth:  sdk.CredentialAuth{Type: sdk.CredentialAuthBearer, Credential: name}})
		case "github-remote-mcp":
			for _, method := range []string{"GET", "POST", "DELETE"} {
				bind(name+"-"+strings.ToLower(method), "api.githubcopilot.com", method, []string{"/mcp", "/mcp/"}, name, sdk.CredentialAuthBearer)
			}
		default:
			return sdk.CredentialVaultCreateRequest{}, errors.New("agent: unsupported service credential target")
		}
		result.Credentials = append(result.Credentials, sdk.Credential{Name: name, Source: sdk.InlineCredentialSource{Value: service.Token}})
	}
	return result, nil
}

// expired reports whether a declared expiry has passed; a credential that
// declares none does not expire.
func expired(expiresAt, now time.Time) bool {
	return !expiresAt.IsZero() && !expiresAt.After(now)
}
