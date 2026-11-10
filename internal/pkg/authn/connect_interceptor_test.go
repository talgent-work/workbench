package authn

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	computerpublicv1 "github.com/talgent-work/contracts/gen/go/computer/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
)

const (
	userID = "2c857667-a533-5efc-b6da-fc9cce51d31b"
	workID = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4d"
)

func method(file protoreflect.FileDescriptor, service, name string) protoreflect.MethodDescriptor {
	return file.Services().ByName(protoreflect.Name(service)).Methods().ByName(protoreflect.Name(name))
}

var (
	// Declared for a user only.
	userOnly = method(agentpublicv1.File_agent_public_v1_personal_work_proto, "PersonalWorkService", "StartPersonalWork")
	// Declared for a user or an agent.
	userOrAgent = method(agentpublicv1.File_agent_public_v1_personal_work_proto, "PersonalWorkService", "ListLibraryFiles")
	// Declared for no caller: a private procedure no entry reaches.
	undeclared = method(agentprivatev1.File_agent_private_v1_acting_work_proto, "ActingWorkService", "AdmitAgentAction")
	// Declared anonymous.
	anonymous = method(computerpublicv1.File_computer_public_v1_registration_proto, "RegistrationService", "StartRegistration")
	// A user-only server-streaming procedure.
	userStream = method(streamgatewaypublicv1.File_streamgateway_public_v1_streamgateway_proto, "StreamGatewayService", "WatchStream")
)

// noStream is a server stream the handlers under test never use.
type noStream struct{}

func (noStream) Receive(any) error  { return nil }
func (noStream) SendHeaders() error { return nil }
func (noStream) Send(any) error     { return nil }

// call dispatches procedure with header through the interceptor and returns
// the Principal its handler received, if it ran.
func call(t *testing.T, interceptor connect.ServerInterceptor, procedure protoreflect.MethodDescriptor, header map[string]string) (principal Principal, ran bool, err error) {
	t.Helper()
	streamType := connect.StreamTypeUnary
	if procedure.IsStreamingServer() {
		streamType = connect.StreamTypeServer
	}
	path := "/" + string(procedure.Parent().FullName()) + "/" + string(procedure.Name())
	server := connect.NewServer(interceptor)
	server.Register(connect.Method{
		Spec: connect.Spec{StreamType: streamType, Procedure: path, Schema: procedure},
		Handler: func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
			principal, _ = PrincipalFrom(ctx)
			ran = true
			return nil
		},
	})
	info := new(connect.CallInfo)
	for key, value := range header {
		info.RequestHeader().Set(key, value)
	}
	err = server.Call(context.Background(), path, info, noStream{})
	return principal, ran, err
}

func userHeaders() map[string]string {
	return map[string]string{
		HeaderAuthenticatedBy: "envoy", HeaderPrincipal: "user", HeaderSubject: " " + userID + " ",
		HeaderDisplayName: "Jace", HeaderRoles: "admin, viewer,",
	}
}

func agentHeaders() map[string]string {
	return map[string]string{
		HeaderAuthenticatedBy: "envoy", HeaderPrincipal: "agent", HeaderSubject: workID,
		HeaderProjectID: "project", HeaderAgentID: "agent-definition", HeaderPurpose: "review",
		HeaderWorkSubjectKind: "case", HeaderWorkSubjectID: "case-1", HeaderRepresentedUserID: userID,
	}
}

// A user's request speaks for exactly that user.
func TestUserRequestSpeaksForItsUser(t *testing.T) {
	principal, ran, err := call(t, NewPrincipalInterceptor(), userOnly, userHeaders())
	require.NoError(t, err)
	require.True(t, ran)
	user, ok := principal.User()
	require.True(t, ok)
	require.Equal(t, userID, user.ID)
	require.Equal(t, "Jace", user.DisplayName)
	require.Equal(t, []string{"admin", "viewer"}, user.Roles)
	_, isAgent := principal.Agent()
	require.False(t, isAgent)
}

// An agent's request speaks for its Work's Agent and never resolves a person,
// not even the one it represents.
func TestAgentRequestNeverResolvesAPerson(t *testing.T) {
	principal, ran, err := call(t, NewPrincipalInterceptor(), userOrAgent, agentHeaders())
	require.NoError(t, err)
	require.True(t, ran)
	agent, ok := principal.Agent()
	require.True(t, ok)
	require.Equal(t, Agent{WorkID: workID, ProjectID: "project", AgentID: "agent-definition", Purpose: "review", SubjectKind: "case", SubjectID: "case-1", RepresentedUserID: userID}, agent)
	_, isUser := principal.User()
	require.False(t, isUser)
	ctx := WithPrincipal(context.Background(), principal)
	_, found := CurrentUserID(ctx)
	require.False(t, found, "an agent's request has no current user")
}

// A procedure admits only the Principals its contract declares; any other is
// refused before the handler runs.
func TestUndeclaredPrincipalIsRefusedBeforeTheHandler(t *testing.T) {
	for name, tc := range map[string]struct {
		procedure protoreflect.MethodDescriptor
		header    map[string]string
	}{
		"agent on a user procedure":    {userOnly, agentHeaders()},
		"user on undeclared procedure": {undeclared, userHeaders()},
		"agent on a user stream":       {userStream, agentHeaders()},
	} {
		t.Run(name, func(t *testing.T) {
			_, ran, err := call(t, NewPrincipalInterceptor(), tc.procedure, tc.header)
			require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
			require.False(t, ran)
		})
	}
	_, ran, err := call(t, NewPrincipalInterceptor(), userStream, userHeaders())
	require.NoError(t, err)
	require.True(t, ran, "a declared stream admits its Principal")
}

// An anonymous procedure admits a caller without any identity, and gives the
// handler no Principal.
func TestAnonymousProcedureAdmitsWithoutPrincipal(t *testing.T) {
	principal, ran, err := call(t, NewPrincipalInterceptor(), anonymous, nil)
	require.NoError(t, err)
	require.True(t, ran)
	_, isUser := principal.User()
	_, isAgent := principal.Agent()
	require.False(t, isUser || isAgent, "the handler runs without a Principal")
}

// Only an identity the entry verified, naming a known Principal with its
// required claims, establishes one.
func TestUnverifiedIdentityIsUnauthenticated(t *testing.T) {
	with := func(base map[string]string, key, value string) map[string]string {
		header := map[string]string{}
		for k, v := range base {
			header[k] = v
		}
		if value == "" {
			delete(header, key)
		} else {
			header[key] = value
		}
		return header
	}
	for name, header := range map[string]map[string]string{
		"no entry marker":        with(userHeaders(), HeaderAuthenticatedBy, ""),
		"no principal":           with(userHeaders(), HeaderPrincipal, ""),
		"unknown principal":      with(userHeaders(), HeaderPrincipal, "service"),
		"no subject":             with(userHeaders(), HeaderSubject, ""),
		"non-uuid subject":       with(userHeaders(), HeaderSubject, "sub-1"),
		"composite subject":      with(userHeaders(), HeaderSubject, "oidc:https://auth.example.com#"+userID),
		"agent without purpose":  with(agentHeaders(), HeaderPurpose, ""),
		"agent without its def":  with(agentHeaders(), HeaderAgentID, ""),
		"invalid represented id": with(agentHeaders(), HeaderRepresentedUserID, "someone"),
		"only a delegated user":  {HeaderDelegatedUserID: userID},
		"only a service":         {HeaderServiceID: "work"},
		"invalid delegated user": {HeaderServiceID: "work", HeaderDelegatedUserID: "issuer#sub-1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, ran, err := call(t, NewPrincipalInterceptor(), userOrAgent, header)
			require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
			require.False(t, ran)
		})
	}
}

// A trusted service's call on a person's behalf speaks for that user.
func TestDelegatedServiceCallSpeaksForItsUser(t *testing.T) {
	principal, _, err := call(t, NewPrincipalInterceptor(), userOnly, map[string]string{HeaderServiceID: "work", HeaderDelegatedUserID: "sub-1"})
	require.NoError(t, err)
	user, ok := principal.User()
	require.True(t, ok)
	require.Equal(t, "sub-1", user.ID)
}

type nativeVerifierFixture struct {
	reject bool
	calls  int
}

func (v *nativeVerifierFixture) VerifyNativeProcess(_ context.Context, workID, credential string) (NativeProcess, error) {
	v.calls++
	if v.reject || workID != "work" || credential != "credential" {
		return NativeProcess{}, connect.NewError(connect.CodeUnauthenticated, "")
	}
	return NativeProcess{WorkID: "work", SubjectID: "native-owner", ProjectID: "project", AgentID: "agent-definition", Purpose: "review"}, nil
}

// A Work's native process speaks for its Work's Agent, whatever person the
// caller claims, and reaches only procedures that admit an agent.
func TestNativeProcessSpeaksForItsWorksAgent(t *testing.T) {
	verifier := &nativeVerifierFixture{}
	interceptor := NewPrincipalInterceptor(WithNativeProcessVerifier(verifier))
	header := map[string]string{HeaderNativeWorkID: "work", HeaderNativeCredential: "credential", HeaderServiceID: "executor", HeaderDelegatedUserID: "spoofed-owner"}
	principal, _, err := call(t, interceptor, userOrAgent, header)
	require.NoError(t, err)
	agent, ok := principal.Agent()
	require.True(t, ok)
	require.Equal(t, "native-owner", agent.RepresentedUserID)
	_, ran, err := call(t, interceptor, userOnly, header)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "valid native proof is no user session")
	require.False(t, ran)
	_, _, err = call(t, interceptor, userStream, header)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "native execution reaches unary procedures only")
	verifier.reject = true
	_, _, err = call(t, interceptor, userOrAgent, header)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "rejected proof must not fall through to service claims")
}

// An identity the entry verified outranks native credentials sent beside it,
// which never reach a service through the entry.
func TestEntryIdentityOutranksNativeCredentials(t *testing.T) {
	verifier := &nativeVerifierFixture{}
	header := agentHeaders()
	header[HeaderNativeWorkID], header[HeaderNativeCredential] = "work", "forged"
	principal, _, err := call(t, NewPrincipalInterceptor(WithNativeProcessVerifier(verifier)), userOrAgent, header)
	require.NoError(t, err)
	agent, ok := principal.Agent()
	require.True(t, ok)
	require.Equal(t, workID, agent.WorkID)
	require.Zero(t, verifier.calls)
}

func TestIncompleteNativeCredentialIsUnauthenticated(t *testing.T) {
	verifier := &nativeVerifierFixture{}
	_, _, err := call(t, NewPrincipalInterceptor(WithNativeProcessVerifier(verifier)), userOrAgent, map[string]string{HeaderNativeWorkID: "work"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Zero(t, verifier.calls)
}
