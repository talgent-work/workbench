package authn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

const (
	HeaderAuthenticatedBy   = "x-talgent-authenticated-by"
	HeaderIssuer            = "x-talgent-issuer"
	HeaderSubject           = "x-talgent-subject"
	HeaderUsername          = "x-talgent-username"
	HeaderEmail             = "x-talgent-email"
	HeaderDisplayName       = "x-talgent-display-name"
	HeaderAvatarURL         = "x-talgent-avatar-url"
	HeaderRoles             = "x-talgent-roles"
	HeaderServiceID         = "x-talgent-service-id"
	HeaderDelegatedUserID   = "x-talgent-delegated-user-id"
	HeaderRuntimeInstanceID = "x-talgent-runtime-instance-id"
	HeaderRuntimePurpose    = "x-talgent-runtime-purpose"
	HeaderRuntimeOwnerRef   = "x-talgent-runtime-owner-ref"
)

// NativeProcessVerifier resolves a Work's native process credential to its delegated user.
type NativeProcessVerifier interface {
	VerifyNativeProcess(ctx context.Context, workID, credential string) (NativeProcess, error)
}

// GatewayActorConfig controls trusted gateway and native process authentication.
type GatewayActorConfig struct {
	// NativeProcedures are the procedures a Work's native process may call:
	// reads constrained to its Project, and the commands that admit its Agent
	// by caller kind.
	NativeProcedures           []string
	NativeProcessVerifier      NativeProcessVerifier
	Enabled                    bool
	ExcludedProcedures         []string
	ServiceOrGatewayProcedures []string
}

// NewConnectUnaryInterceptor authenticates gateway-backed Connect calls — both
// unary and server-streaming handlers — and writes the authenticated user into
// the request context. Streaming support is required for streamgateway's
// protected streaming RPCs; unary behavior is unchanged for existing callers.
func NewConnectUnaryInterceptor(cfg GatewayActorConfig) connect.Interceptor {
	excluded := make(map[string]struct{}, len(cfg.ExcludedProcedures))
	for _, procedure := range cfg.ExcludedProcedures {
		excluded[procedure] = struct{}{}
	}
	serviceOrGateway := make(map[string]struct{}, len(cfg.ServiceOrGatewayProcedures))
	for _, procedure := range cfg.ServiceOrGatewayProcedures {
		serviceOrGateway[procedure] = struct{}{}
	}
	return &gatewayActorInterceptor{
		cfg:              cfg,
		excluded:         excluded,
		serviceOrGateway: serviceOrGateway,
	}
}

type gatewayActorInterceptor struct {
	cfg              GatewayActorConfig
	excluded         map[string]struct{}
	serviceOrGateway map[string]struct{}
}

func (i *gatewayActorInterceptor) skip(procedure string, header http.Header) bool {
	if !i.cfg.Enabled {
		return true
	}
	if _, ok := i.serviceOrGateway[procedure]; ok {
		return strings.TrimSpace(header.Get(HeaderServiceID)) != ""
	}
	_, ok := i.excluded[procedure]
	return ok
}

func (i *gatewayActorInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if hasNativeCredential(req.Header()) {
			process, err := i.authenticateNative(ctx, req.Header())
			if err != nil {
				return nil, err
			}
			if !containsProcedure(i.cfg.NativeProcedures, req.Spec().Procedure) {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("native process cannot access this procedure"))
			}
			ctx = WithNativeProcess(ctx, process)
			return next(WithAuthenticatedUser(ctx, AuthenticatedUser{ID: process.SubjectID}), req)
		}
		if i.skip(req.Spec().Procedure, req.Header()) {
			return next(ctx, req)
		}
		user, err := authenticateGatewayUser(req.Header())
		if err != nil {
			return nil, err
		}
		return next(WithAuthenticatedUser(ctx, user), req)
	}
}

func (i *gatewayActorInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *gatewayActorInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if hasNativeCredential(conn.RequestHeader()) {
			return connect.NewError(connect.CodePermissionDenied, errors.New("native execution cannot access streaming procedures"))
		}
		if i.skip(conn.Spec().Procedure, conn.RequestHeader()) {
			return next(ctx, conn)
		}
		user, err := authenticateGatewayUser(conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(WithAuthenticatedUser(ctx, user), conn)
	}
}

// authenticateGatewayUser extracts the trusted gateway user identity from request
// headers. Shared by unary and streaming handler paths.
func authenticateGatewayUser(header http.Header) (AuthenticatedUser, error) {
	serviceID := strings.TrimSpace(header.Get(HeaderServiceID))
	delegatedUserID := strings.TrimSpace(header.Get(HeaderDelegatedUserID))
	if serviceID != "" && delegatedUserID != "" {
		userID, ok := normalizeUserID(delegatedUserID)
		if !ok {
			return AuthenticatedUser{}, connect.NewError(connect.CodeUnauthenticated, errors.New("delegated user id is invalid"))
		}
		return AuthenticatedUser{ID: userID}, nil
	}
	if serviceID != "" || delegatedUserID != "" {
		return AuthenticatedUser{}, connect.NewError(connect.CodeUnauthenticated, errors.New("delegated user headers are incomplete"))
	}

	if strings.TrimSpace(header.Get(HeaderAuthenticatedBy)) != "envoy" {
		return AuthenticatedUser{}, connect.NewError(connect.CodeUnauthenticated, errors.New("trusted gateway user is required"))
	}
	subject := strings.TrimSpace(header.Get(HeaderSubject))
	userID, ok := normalizeGatewaySubjectID(subject)
	if !ok {
		return AuthenticatedUser{}, connect.NewError(connect.CodeUnauthenticated, errors.New("account subject is required"))
	}

	return AuthenticatedUser{
		ID:           userID,
		Username:     strings.TrimSpace(header.Get(HeaderUsername)),
		PrimaryEmail: strings.TrimSpace(header.Get(HeaderEmail)),
		DisplayName:  strings.TrimSpace(header.Get(HeaderDisplayName)),
		AvatarURL:    strings.TrimSpace(header.Get(HeaderAvatarURL)),
		Roles:        parseRoles(header.Get(HeaderRoles)),
	}, nil
}

// normalizeGatewaySubjectID admits an Account id: a canonical lowercase UUID.
func normalizeGatewaySubjectID(id string) (string, bool) {
	id, ok := normalizeUserID(id)
	if !ok {
		return "", false
	}
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

func hasNativeCredential(header http.Header) bool {
	return header.Get(HeaderNativeWorkID) != "" || header.Get(HeaderNativeCredential) != ""
}
func (i *gatewayActorInterceptor) authenticateNative(ctx context.Context, header http.Header) (NativeProcess, error) {
	workID, credential := strings.TrimSpace(header.Get(HeaderNativeWorkID)), strings.TrimSpace(header.Get(HeaderNativeCredential))
	if workID == "" || credential == "" || i.cfg.NativeProcessVerifier == nil {
		return NativeProcess{}, connect.NewError(connect.CodeUnauthenticated, errors.New("complete native process credentials required"))
	}
	return i.cfg.NativeProcessVerifier.VerifyNativeProcess(ctx, workID, credential)
}

func containsProcedure(procedures []string, procedure string) bool {
	for _, allowed := range procedures {
		if allowed == procedure {
			return true
		}
	}
	return false
}
