package authn

import (
	"context"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	admissionv1 "github.com/talgent-work/contracts/gen/go/admission/v1"
)

// The identity headers the entry writes from a verified Identity Token, after
// removing any a client sent (contracts docs/gateway-identity.md). A claim the
// token lacks writes no header.
const (
	HeaderAuthenticatedBy = "x-talgent-authenticated-by"
	HeaderIssuer          = "x-talgent-issuer"
	HeaderPrincipal       = "x-talgent-principal"
	HeaderSubject         = "x-talgent-subject"
	// A user's claims.
	HeaderUsername    = "x-talgent-username"
	HeaderEmail       = "x-talgent-email"
	HeaderDisplayName = "x-talgent-display-name"
	HeaderAvatarURL   = "x-talgent-avatar-url"
	HeaderRoles       = "x-talgent-roles"
	// An agent's claims: its Work's fixed facts.
	HeaderProjectID         = "x-talgent-project-id"
	HeaderAgentID           = "x-talgent-agent-id"
	HeaderPurpose           = "x-talgent-purpose"
	HeaderWorkSubjectKind   = "x-talgent-work-subject-kind"
	HeaderWorkSubjectID     = "x-talgent-work-subject-id"
	HeaderRepresentedUserID = "x-talgent-represented-user-id"
	// A trusted service's call on a person's behalf.
	HeaderServiceID         = "x-talgent-service-id"
	HeaderDelegatedUserID   = "x-talgent-delegated-user-id"
	HeaderRuntimeInstanceID = "x-talgent-runtime-instance-id"
	HeaderRuntimePurpose    = "x-talgent-runtime-purpose"
	HeaderRuntimeOwnerRef   = "x-talgent-runtime-owner-ref"
)

// The values of HeaderPrincipal.
const (
	principalUser  = "user"
	principalAgent = "agent"
)

// NativeProcessVerifier resolves a Work's native process credential.
type NativeProcessVerifier interface {
	VerifyNativeProcess(ctx context.Context, workID, credential string) (NativeProcess, error)
}

// Option configures the Principal interceptor.
type Option func(*principalInterceptor)

// WithNativeProcessVerifier lets a Work's native process, verified by its
// Work credential, call as its Work's agent Principal. It goes with the native
// API.
func WithNativeProcessVerifier(verifier NativeProcessVerifier) Option {
	return func(i *principalInterceptor) { i.native = verifier }
}

// NewPrincipalInterceptor establishes exactly one Principal for every request,
// from the identity headers the entry writes or a trusted service's
// delegation, and admits only the Principals the procedure's contract
// declares (admission.v1.admits). A procedure that declares nothing refuses
// every caller; an anonymous one admits a caller without a Principal. Both
// unary and streaming procedures pass through it.
func NewPrincipalInterceptor(options ...Option) connect.ServerInterceptor {
	i := &principalInterceptor{}
	for _, option := range options {
		option(i)
	}
	return i.wrap
}

type principalInterceptor struct {
	native NativeProcessVerifier
}

// headerReader is what the interceptor reads of a request's metadata, as both
// net/http and Connect carry it.
type headerReader interface {
	Get(key string) string
}

func (i *principalInterceptor) wrap(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		admission, declared := admissionOf(spec)
		if !declared {
			return connect.NewError(connect.CodePermissionDenied, "the procedure admits no caller")
		}
		if admission.GetAnonymous() {
			return next(ctx, spec, stream)
		}
		header := headerReader(new(connect.Header))
		if info, ok := connect.CallInfoForServerContext(ctx); ok {
			header = info.RequestHeader()
		}
		var principal Principal
		var err error
		// An identity the entry verified outranks native credentials, which
		// reach a service only through the native API, never the entry.
		if hasNativeCredential(header) && strings.TrimSpace(header.Get(HeaderAuthenticatedBy)) != "envoy" {
			if spec.StreamType != connect.StreamTypeUnary {
				return connect.NewError(connect.CodePermissionDenied, "native execution cannot access streaming procedures")
			}
			ctx, principal, err = i.authenticateNative(ctx, header)
		} else {
			principal, err = establish(header)
		}
		if err != nil {
			return err
		}
		if !admits(admission, principal) {
			return connect.NewError(connect.CodePermissionDenied, "the procedure does not admit this caller")
		}
		return next(WithPrincipal(ctx, principal), spec, stream)
	}
}

// admissionOf is the procedure's declared admission.
func admissionOf(spec connect.Spec) (*admissionv1.Admission, bool) {
	method, ok := spec.Schema.(protoreflect.MethodDescriptor)
	if !ok || !proto.HasExtension(method.Options(), admissionv1.E_Admits) {
		return nil, false
	}
	admission, ok := proto.GetExtension(method.Options(), admissionv1.E_Admits).(*admissionv1.Admission)
	return admission, ok && admission != nil
}

func admits(admission *admissionv1.Admission, principal Principal) bool {
	want := admissionv1.Principal_PRINCIPAL_USER
	if _, agent := principal.Agent(); agent {
		want = admissionv1.Principal_PRINCIPAL_AGENT
	}
	return slices.Contains(admission.GetPrincipals(), want)
}

// establish builds the request's one Principal: a trusted service's
// delegated person, or the user or agent the entry verified. Delegation is
// read first and needs no further proof: the cluster network is its trust
// boundary, and the entry strips its headers from every public request
// (contracts docs/gateway-identity.md).
func establish(header headerReader) (Principal, error) {
	if userID, delegated, err := delegatedUser(header); delegated || err != nil {
		return UserPrincipal(AuthenticatedUser{ID: userID}), err
	}
	if strings.TrimSpace(header.Get(HeaderAuthenticatedBy)) != "envoy" {
		return Principal{}, connect.NewError(connect.CodeUnauthenticated, "an identity verified by the entry is required")
	}
	subject, ok := canonicalUUID(header.Get(HeaderSubject))
	if !ok {
		return Principal{}, connect.NewError(connect.CodeUnauthenticated, "the identity's subject is required")
	}
	switch strings.TrimSpace(header.Get(HeaderPrincipal)) {
	case principalUser:
		return UserPrincipal(AuthenticatedUser{
			ID:           subject,
			Username:     strings.TrimSpace(header.Get(HeaderUsername)),
			PrimaryEmail: strings.TrimSpace(header.Get(HeaderEmail)),
			DisplayName:  strings.TrimSpace(header.Get(HeaderDisplayName)),
			AvatarURL:    strings.TrimSpace(header.Get(HeaderAvatarURL)),
			Roles:        parseRoles(header.Get(HeaderRoles)),
		}), nil
	case principalAgent:
		agent := Agent{
			WorkID:      subject,
			ProjectID:   strings.TrimSpace(header.Get(HeaderProjectID)),
			AgentID:     strings.TrimSpace(header.Get(HeaderAgentID)),
			Purpose:     strings.TrimSpace(header.Get(HeaderPurpose)),
			SubjectKind: strings.TrimSpace(header.Get(HeaderWorkSubjectKind)),
			SubjectID:   strings.TrimSpace(header.Get(HeaderWorkSubjectID)),
		}
		if represented := strings.TrimSpace(header.Get(HeaderRepresentedUserID)); represented != "" {
			if agent.RepresentedUserID, ok = canonicalUUID(represented); !ok {
				return Principal{}, connect.NewError(connect.CodeUnauthenticated, "the agent's represented person is invalid")
			}
		}
		if agent.AgentID == "" || agent.Purpose == "" {
			return Principal{}, connect.NewError(connect.CodeUnauthenticated, "the agent's AgentDefinition and purpose are required")
		}
		return AgentPrincipal(agent), nil
	default:
		return Principal{}, connect.NewError(connect.CodeUnauthenticated, "the identity names no known Principal")
	}
}

// DelegatedUserID is the person a trusted service's call speaks for. A
// private procedure, which no entry reaches, reads it after authenticating
// the service itself.
func DelegatedUserID(header *connect.Header) (string, bool) {
	userID, delegated, err := delegatedUser(header)
	return userID, delegated && err == nil
}

// delegatedUser reads a trusted service's delegation: both its id and the
// person it acts for, or neither.
func delegatedUser(header headerReader) (string, bool, error) {
	serviceID := strings.TrimSpace(header.Get(HeaderServiceID))
	delegatedUserID := strings.TrimSpace(header.Get(HeaderDelegatedUserID))
	if serviceID == "" && delegatedUserID == "" {
		return "", false, nil
	}
	if serviceID == "" || delegatedUserID == "" {
		return "", false, connect.NewError(connect.CodeUnauthenticated, "delegated user headers are incomplete")
	}
	userID, ok := normalizeUserID(delegatedUserID)
	if !ok {
		return "", false, connect.NewError(connect.CodeUnauthenticated, "delegated user id is invalid")
	}
	return userID, true, nil
}

// canonicalUUID admits an Account or Work id: a canonical lowercase UUID.
func canonicalUUID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", false
	}
	return id, true
}

func parseRoles(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	roles := make([]string, 0, len(parts))
	for _, part := range parts {
		role := strings.TrimSpace(part)
		if role != "" {
			roles = append(roles, role)
		}
	}
	return roles
}

func hasNativeCredential(header headerReader) bool {
	return header.Get(HeaderNativeWorkID) != "" || header.Get(HeaderNativeCredential) != ""
}

// authenticateNative verifies a native process and speaks for its Work's
// Agent; the process context stays beside it for the Artifact commands that
// read its Session Workspace.
func (i *principalInterceptor) authenticateNative(ctx context.Context, header headerReader) (context.Context, Principal, error) {
	workID, credential := strings.TrimSpace(header.Get(HeaderNativeWorkID)), strings.TrimSpace(header.Get(HeaderNativeCredential))
	if workID == "" || credential == "" || i.native == nil {
		return ctx, Principal{}, connect.NewError(connect.CodeUnauthenticated, "complete native process credentials required")
	}
	process, err := i.native.VerifyNativeProcess(ctx, workID, credential)
	if err != nil {
		return ctx, Principal{}, err
	}
	return WithNativeProcess(ctx, process), AgentPrincipal(Agent{
		WorkID: process.WorkID, ProjectID: process.ProjectID, AgentID: process.AgentID, Purpose: process.Purpose,
		SubjectKind: process.WorkSubjectKind, SubjectID: process.WorkSubjectID, RepresentedUserID: process.SubjectID,
	}), nil
}
