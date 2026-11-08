package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"xorm.io/xorm"
)

// Normal management responses needed by the pre-existing allocation, deletion
// and native handoff tests after credential preparation became mandatory.
type vaultFixture struct {
	mu     sync.Mutex
	state  *sdk.CredentialVaultState
	policy *sdk.NetworkPolicy
}

func (f *vaultFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("OPENSANDBOX-EGRESS-AUTH") != "egress-secret" {
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(200)
		return
	case "/policy":
		_ = json.NewEncoder(w).Encode(sdk.PolicyStatusResponse{EnforcementMode: "dns+nft", Policy: f.policy})
		return
	case "/credential-vault":
	default:
		w.WriteHeader(404)
		return
	}
	if r.Method == "GET" {
		if f.state == nil {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(f.state)
		return
	}
	var credentials []sdk.Credential
	var bindings []sdk.CredentialBinding
	revision := 1
	switch r.Method {
	case "POST":
		var plan sdk.CredentialVaultCreateRequest
		if json.NewDecoder(r.Body).Decode(&plan) != nil {
			w.WriteHeader(400)
			return
		}
		credentials, bindings = plan.Credentials, plan.Bindings
	case "PATCH":
		if f.state == nil {
			w.WriteHeader(404)
			return
		}
		var patch sdk.CredentialVaultPatchRequest
		if json.NewDecoder(r.Body).Decode(&patch) != nil {
			w.WriteHeader(400)
			return
		}
		revision = f.state.Revision + 1
		// Production replaces its complete owned plan on every successful refresh.
		if patch.Credentials != nil {
			credentials = append(patch.Credentials.Add, patch.Credentials.Replace...)
		}
		if patch.Bindings != nil {
			bindings = append(patch.Bindings.Add, patch.Bindings.Replace...)
		}
	default:
		w.WriteHeader(405)
		return
	}
	f.state = &sdk.CredentialVaultState{Revision: revision, Credentials: []sdk.CredentialMetadata{}, Bindings: []sdk.CredentialBindingMetadata{}}
	for _, v := range credentials {
		f.state.Credentials = append(f.state.Credentials, sdk.CredentialMetadata{Name: v.Name, SourceType: "inline", Revision: revision})
	}
	for _, v := range bindings {
		match := v.Match
		f.state.Bindings = append(f.state.Bindings, sdk.CredentialBindingMetadata{Name: v.Name, Match: &match, Auth: &sdk.CredentialAuthMetadata{Type: string(v.Auth.Type)}, Revision: revision})
	}
	_ = json.NewEncoder(w).Encode(f.state)
}

func fixtureSandboxCredentials(db *xorm.Engine, s *Sandboxes) *SandboxCredentials {
	return &SandboxCredentials{db: db, lifecycle: s.client, models: s.config.Models, records: records{db: db}, http: http.DefaultClient, transport: "encrypted-network"}
}

// admitting is the holding check of an execution that still admits every
// delivery.
func admitting(context.Context) error { return nil }
