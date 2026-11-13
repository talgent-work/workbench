package authn

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	_ "google.golang.org/protobuf/types/known/emptypb"

	admissionv1 "github.com/talgent-work/contracts/gen/go/admission/v1"
)

const applicationID = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"

// declared builds a procedure whose contract admits exactly principals, as a
// public procedure that opens itself to Applications would declare.
func declared(t *testing.T, name string, principals ...admissionv1.Principal) protoreflect.MethodDescriptor {
	t.Helper()
	options := &descriptorpb.MethodOptions{}
	proto.SetExtension(options, admissionv1.E_Admits, &admissionv1.Admission{Principals: principals})
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       proto.String("admission_test/" + name + ".proto"),
		Package:    proto.String("admissiontest." + name),
		Dependency: []string{"google/protobuf/empty.proto"},
		Syntax:     proto.String("proto3"),
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("TestService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("Call"),
				InputType:  proto.String(".google.protobuf.Empty"),
				OutputType: proto.String(".google.protobuf.Empty"),
				Options:    options,
			}},
		}},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)
	return file.Services().Get(0).Methods().Get(0)
}

func applicationHeaders() map[string]string {
	return map[string]string{HeaderAuthenticatedBy: "envoy", HeaderPrincipal: "application", HeaderSubject: applicationID}
}

// An Application's request speaks for that Application alone: never a person,
// not even its owner, and never an agent.
func TestApplicationRequestSpeaksForItsApplication(t *testing.T) {
	principal, ran, err := call(t, NewPrincipalInterceptor(), declared(t, "application", admissionv1.Principal_PRINCIPAL_APPLICATION), applicationHeaders())
	require.NoError(t, err)
	require.True(t, ran)
	application, ok := principal.Application()
	require.True(t, ok)
	require.Equal(t, Application{ID: applicationID}, application)
	_, isUser := principal.User()
	_, isAgent := principal.Agent()
	require.False(t, isUser || isAgent)
	ctx := WithPrincipal(context.Background(), principal)
	_, found := CurrentUserID(ctx)
	require.False(t, found, "an Application's request has no current user")
	current, found := CurrentApplication(ctx)
	require.True(t, found)
	require.Equal(t, applicationID, current.ID)
}

// A procedure admits an Application only when it declares it; the user and
// agent procedures that exist today refuse it before the handler runs.
func TestApplicationIsAdmittedOnlyWhereDeclared(t *testing.T) {
	for name, procedure := range map[string]protoreflect.MethodDescriptor{
		"user procedure":           userOnly,
		"user or agent procedure":  userOrAgent,
		"undeclared procedure":     undeclared,
		"user and agent, declared": declared(t, "people_and_agents", admissionv1.Principal_PRINCIPAL_USER, admissionv1.Principal_PRINCIPAL_AGENT),
	} {
		t.Run(name, func(t *testing.T) {
			_, ran, err := call(t, NewPrincipalInterceptor(), procedure, applicationHeaders())
			require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
			require.False(t, ran)
		})
	}
	// A server stream that declares the Application admits it too.
	_, ran, err := call(t, NewPrincipalInterceptor(), ownerStream, applicationHeaders())
	require.NoError(t, err)
	require.True(t, ran)
	// Declaring the Application admits it beside the others it names, and
	// still refuses those it does not.
	shared := declared(t, "shared", admissionv1.Principal_PRINCIPAL_USER, admissionv1.Principal_PRINCIPAL_APPLICATION)
	_, ran, err = call(t, NewPrincipalInterceptor(), shared, applicationHeaders())
	require.NoError(t, err)
	require.True(t, ran)
	_, ran, err = call(t, NewPrincipalInterceptor(), shared, userHeaders())
	require.NoError(t, err)
	require.True(t, ran)
	_, ran, err = call(t, NewPrincipalInterceptor(), shared, agentHeaders())
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.False(t, ran)
}

// An Application's identity needs a canonical subject like every Principal.
func TestApplicationWithoutAValidSubjectIsUnauthenticated(t *testing.T) {
	procedure := declared(t, "application_subject", admissionv1.Principal_PRINCIPAL_APPLICATION)
	for name, subject := range map[string]string{"none": "", "not a uuid": "app-1", "uppercase": "0193A7C2-6F1E-7C2A-9B3D-5E8F1A2B3C4E"} {
		t.Run(name, func(t *testing.T) {
			header := applicationHeaders()
			header[HeaderSubject] = subject
			_, ran, err := call(t, NewPrincipalInterceptor(), procedure, header)
			require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
			require.False(t, ran)
		})
	}
}

// An Application's Work's Agent states which Application owns its Work and
// still speaks only for that Agent: neither for the Application nor for a
// person.
func TestAgentOfAnApplicationsWorkNamesItsApplicationOnly(t *testing.T) {
	header := map[string]string{
		HeaderAuthenticatedBy: "envoy", HeaderPrincipal: "agent", HeaderSubject: workID,
		HeaderWorkCaller: "application:" + applicationID, HeaderAgentID: "agent-definition", HeaderPurpose: "work", HeaderApplicationID: applicationID,
	}
	principal, ran, err := call(t, NewPrincipalInterceptor(), userOrAgent, header)
	require.NoError(t, err)
	require.True(t, ran)
	agent, ok := principal.Agent()
	require.True(t, ok)
	require.Equal(t, Agent{WorkID: workID, WorkCaller: WorkFact{Kind: "application", ID: applicationID}, AgentID: "agent-definition", Purpose: "work", ApplicationID: applicationID}, agent)
	_, isApplication := principal.Application()
	require.False(t, isApplication, "the Agent never speaks for the Application")
}
