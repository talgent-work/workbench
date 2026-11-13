package authn

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
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
	userOnly = method(agentpublicv2.File_agent_public_v2_work_proto, "WorkService", "GrantWorkShare")
	// Declared for a user or an agent.
	userOrAgent = method(agentpublicv2.File_agent_public_v2_work_proto, "WorkService", "ListLibraryFiles")
	// Declared for no caller: a private procedure no entry reaches.
	undeclared = method(agentprivatev1.File_agent_private_v1_acting_work_proto, "ActingWorkService", "AdmitAgentAction")
	// Declared anonymous.
	anonymous = method(computerpublicv1.File_computer_public_v1_registration_proto, "RegistrationService", "StartRegistration")
	// A server-streaming procedure declared for a person or an Application.
	ownerStream = method(streamgatewaypublicv1.File_streamgateway_public_v1_streamgateway_proto, "StreamGatewayService", "WatchStream")
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
		HeaderWorkCaller: " project : p1 ", HeaderWorkKey: "case:case:1", HeaderAgentID: "agent-definition", HeaderPurpose: "review",
		HeaderRepresentedUserID: userID,
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
	require.Equal(t, Agent{
		WorkID: workID, WorkCaller: WorkFact{Kind: "project", ID: "p1"}, WorkKey: WorkFact{Kind: "case", ID: "case:1"},
		AgentID: "agent-definition", Purpose: "review", RepresentedUserID: userID,
	}, agent, "each Work fact splits at its first colon, and a Work without a File Home has none")
	require.Equal(t, "case:case:1", agent.WorkKey.String())
	require.Empty(t, agent.FileHome.String(), "no File Home, no value")
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
		"agent on a user stream":       {ownerStream, agentHeaders()},
	} {
		t.Run(name, func(t *testing.T) {
			_, ran, err := call(t, NewPrincipalInterceptor(), tc.procedure, tc.header)
			require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
			require.False(t, ran)
		})
	}
	_, ran, err := call(t, NewPrincipalInterceptor(), ownerStream, userHeaders())
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
		"invalid application id": with(agentHeaders(), HeaderApplicationID, "someone"),
		"agent without a caller": with(agentHeaders(), HeaderWorkCaller, ""),
		"caller without a kind":  with(agentHeaders(), HeaderWorkCaller, ":p1"),
		"caller without an id":   with(agentHeaders(), HeaderWorkCaller, "project: "),
		"caller without a colon": with(agentHeaders(), HeaderWorkCaller, "project"),
		"caller kind too long":   with(agentHeaders(), HeaderWorkCaller, strings.Repeat("k", 33)+":p1"),
		"malformed work key":     with(agentHeaders(), HeaderWorkKey, "case"),
		"malformed file home":    with(agentHeaders(), HeaderFileHome, "case:"),
		"only a delegated user":  {HeaderDelegatedUserID: userID},
		"only a service":         {HeaderServiceID: "work"},
		"invalid delegated user": {HeaderServiceID: "work", HeaderDelegatedUserID: "issuer#sub-1"},
		// A Work credential is no identity: Agents reach a service only
		// through the entry.
		"a Work credential": {"x-talgent-work-id": workID, "x-talgent-work-credential": "credential"},
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

// A delegated call's reader admission names a Work Caller as "<kind>:<id>";
// any other request, or any other form, admits nothing.
func TestReaderAdmissionNamesAWorkCaller(t *testing.T) {
	header := func(values map[string]string) *connect.Header {
		h := new(connect.Header)
		for key, value := range values {
			h.Set(key, value)
		}
		return h
	}
	delegated := func(admission string) *connect.Header {
		return header(map[string]string{HeaderServiceID: "work", HeaderDelegatedUserID: userID, HeaderReaderAdmission: admission})
	}
	kind, id, ok := ReaderAdmission(delegated(" project:p:1 "))
	require.True(t, ok)
	require.Equal(t, []string{"project", "p:1"}, []string{kind, id})
	for name, h := range map[string]*connect.Header{
		"not delegated":   header(map[string]string{HeaderReaderAdmission: "project:p1"}),
		"reference=value": delegated("project=p1"),
		"no kind":         delegated(":p1"),
		"no id":           delegated("project:"),
		"none":            delegated(""),
	} {
		_, _, ok := ReaderAdmission(h)
		require.False(t, ok, name)
	}
}
