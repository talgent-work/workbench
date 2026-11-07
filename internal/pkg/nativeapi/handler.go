// Package nativeapi serves the exact product procedures an Agent reaches, each
// called under the Agent's own Work credential: by the Agent directly over
// HTTP, or by the platform Connector on its behalf.
package nativeapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/storagepath"
)

// AccessLinkPath is where Storage serves Access Links; a Work downloads the
// files it was issued links for through it.
const AccessLinkPath = storagepath.ContentPath

// IdentityPath answers which Work a credential belongs to as soon as its
// sandbox is live, before the Work's first turn: the Gateway lists tools
// and the Work's hooks read through them then.
const IdentityPath = "/talgent.native.v1.NativeService/Identity"

// Identity is a live Work as IdentityPath reports it.
type Identity struct {
	WorkID    string `json:"work_id"`
	ProjectID string `json:"project_id"`
	// SubjectKind and SubjectID name the Work's Subject.
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Purpose     string `json:"purpose"`
}

type Config struct {
	Addr string `json:"addr" yaml:"addr" toml:"addr"`
	// GatewayToken, when set, is the secret the platform Connector presents
	// on every request, and only the Connector reaches the API; sandboxes never
	// hold it. Empty, every caller presenting a current Work credential
	// reaches the API directly.
	GatewayToken string `json:"gateway-token" yaml:"gateway-token" toml:"gateway-token"`
}

// Validate requires the Gateway's token, for a native API only the Gateway
// reaches: without it any caller holding a Work credential could go around
// the Gateway.
func (c Config) Validate() error {
	if strings.TrimSpace(c.GatewayToken) == "" {
		return errors.New("native API requires the Gateway token")
	}
	return nil
}

// ReadProcedures are controlplane read-only procedures verified against the
// Work's live sandbox rather than its interaction state, so a session can
// read before its first turn.
type ReadProcedures struct {
	Procedures []string
	Verifier   authn.NativeProcessVerifier
}

// Calls are the product procedures an Agent reaches under its Work's
// current credential. Each context lists its own; the native API keeps none.
type Calls struct {
	Procedures []string
	// SpaceTokenProcedures are the calls that also carry the Work Agent's
	// Space Token; no other call may.
	SpaceTokenProcedures []string
}

// NewHandler registers exact native procedures, never a general service prefix.
// Every request must carry the Agent's current Work credential, and come from
// the Gateway when gatewayToken is set; each product context admits the
// Agent behind it.
func NewHandler(controlplane http.Handler, gatewayToken string, verifier authn.NativeProcessVerifier, calls Calls, reads ReadProcedures) (http.Handler, error) {
	gatewayToken = strings.TrimSpace(gatewayToken)
	if verifier == nil || controlplane == nil {
		return nil, errors.New("native API requires the controlplane and an execution verifier")
	}
	if reads.Verifier == nil {
		return nil, errors.New("the native API requires a live Work verifier for identity and reads")
	}
	routes := make(map[string]http.Handler)
	for _, procedure := range slices.Concat(calls.Procedures, calls.SpaceTokenProcedures) {
		routes[procedure] = controlplane
	}
	readOnly := make(map[string]bool, len(reads.Procedures)+1)
	for _, procedure := range reads.Procedures {
		routes[procedure], readOnly[procedure] = controlplane, true
	}
	routes[IdentityPath], readOnly[IdentityPath] = http.HandlerFunc(identity), true
	spaceTokens := make(map[string]bool, len(calls.SpaceTokenProcedures))
	for _, procedure := range calls.SpaceTokenProcedures {
		spaceTokens[procedure] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next, allowed := routes[r.URL.Path]
		// An Access Link the Work was issued is read through its signed grant,
		// still only by a current Work.
		download := r.URL.Path == AccessLinkPath && r.Method == http.MethodGet
		if download {
			next, allowed = controlplane, true
		}
		if !allowed || r.URL.EscapedPath() != r.URL.Path || (r.URL.RawQuery != "" && !download) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost && !download {
			writeError(w, r, connect.CodePermissionDenied, "native API accepts unary POST requests only")
			return
		}
		// Without a Gateway token the Work credential alone admits the caller,
		// and a Gateway token presented beside it is dropped unread.
		presented := r.Header.Values(authn.HeaderGatewayToken)
		if gatewayToken != "" && (len(presented) != 1 || subtle.ConstantTimeCompare([]byte(presented[0]), []byte(gatewayToken)) != 1) {
			writeError(w, r, connect.CodePermissionDenied, "the native API serves only the platform Connector")
			return
		}
		r.Header.Del(authn.HeaderGatewayToken)
		// A direct caller's Credential Proxy adds the Space Token to every
		// request it sends here; only the procedures that take it receive it.
		if gatewayToken == "" && !spaceTokens[r.URL.Path] {
			r.Header.Del(authn.HeaderSpaceToken)
		}
		// Native credentials are the only accepted identity. Reject trusted headers
		// before a general business interceptor can interpret their claims. The
		// headers Envoy adds on the public route a Managed Work calls through
		// carry no identity; they are dropped unread.
		for key := range r.Header {
			lower := strings.ToLower(key)
			if strings.HasPrefix(lower, "x-envoy-") {
				r.Header.Del(key)
				continue
			}
			native := lower == strings.ToLower(authn.HeaderNativeWorkID) || lower == strings.ToLower(authn.HeaderNativeCredential) ||
				(spaceTokens[r.URL.Path] && lower == strings.ToLower(authn.HeaderSpaceToken))
			if (!native && strings.HasPrefix(lower, "x-talgent-")) || lower == "authorization" || lower == "cookie" {
				writeError(w, r, connect.CodeUnauthenticated, "native process credentials are the only accepted identity")
				return
			}
		}
		workID, credential := strings.TrimSpace(r.Header.Get(authn.HeaderNativeWorkID)), strings.TrimSpace(r.Header.Get(authn.HeaderNativeCredential))
		if workID == "" || credential == "" || len(r.Header.Values(authn.HeaderNativeWorkID)) != 1 || len(r.Header.Values(authn.HeaderNativeCredential)) != 1 {
			writeError(w, r, connect.CodeUnauthenticated, "complete native process credentials required")
			return
		}
		verify := verifier
		if readOnly[r.URL.Path] {
			verify = reads.Verifier
		}
		identity, err := verify.VerifyNativeProcess(r.Context(), workID, credential)
		if err != nil || identity.WorkID != workID || identity.SubjectID == "" {
			writeError(w, r, connect.CodeUnauthenticated, "current native process credential required")
			return
		}
		next.ServeHTTP(w, r.WithContext(authn.WithNativeProcess(r.Context(), identity)))
	}), nil
}

// identity reports the verified live Work.
func identity(w http.ResponseWriter, r *http.Request) {
	process, _ := authn.NativeProcessFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Identity{WorkID: process.WorkID, ProjectID: process.ProjectID, SubjectKind: process.WorkSubjectKind, SubjectID: process.WorkSubjectID, Purpose: process.Purpose})
}

func writeError(w http.ResponseWriter, r *http.Request, code connect.Code, message string) {
	_ = connecthttp.NewErrorWriter().Write(w, r, connect.NewError(code, message))
}
