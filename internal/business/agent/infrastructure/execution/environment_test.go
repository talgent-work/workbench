package execution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

type resourceCall struct {
	create  domain.StartExecutionRequest
	mounts  domain.SandboxMounts
	managed bool
	deleted string
	retired string
}
type environmentServer struct {
	calls     chan resourceCall
	failure   error
	abandoned bool
}

func (s *environmentServer) Create(_ context.Context, request domain.StartExecutionRequest, mounts domain.SandboxMounts) error {
	s.calls <- resourceCall{create: request, mounts: mounts}
	return s.failure
}
func (s *environmentServer) CreateManaged(_ context.Context, request domain.StartExecutionRequest) error {
	s.calls <- resourceCall{create: request, managed: true}
	return s.failure
}
func (s *environmentServer) Delete(_ context.Context, workID string) error {
	s.calls <- resourceCall{deleted: workID}
	return s.failure
}
func (s *environmentServer) RetireFenced(_ context.Context, workID string) error {
	s.calls <- resourceCall{retired: workID}
	return s.failure
}
func (s *environmentServer) StartRejected(context.Context, string) (bool, error) {
	return false, nil
}
func (s *environmentServer) StartAbandoned(context.Context, string) (bool, error) {
	return s.abandoned, nil
}

// storageMounts grants each start's spaces by its business identity, as
// Storage's Sandbox Mount Contract does.
type storageMounts struct{ requests []domain.SandboxMountRequest }

func (s *storageMounts) AcquireMounts(_ context.Context, request domain.SandboxMountRequest) (domain.SandboxMounts, error) {
	s.requests = append(s.requests, request)
	mounts := domain.SandboxMounts{Workspace: &domain.MountGrant{ClaimName: "nas", SubPath: "workspaces/" + request.WorkspaceReference}, Home: domain.MountGrant{ClaimName: "nas", SubPath: "homes/" + request.HomeOwner}}
	for _, mount := range request.ReadOnlyMounts {
		if mounts.ReadOnly == nil {
			mounts.ReadOnly = map[string]domain.MountGrant{}
		}
		mounts.ReadOnly[mount.Name] = domain.MountGrant{ClaimName: "inputs", SubPath: "references/" + mount.Reference, ReadOnly: true}
	}
	return mounts, nil
}

// computerPlaces describes each Computer as the fixture names it: Managed
// when listed, a Cloud otherwise.
type computerPlaces map[string]command.ComputerDescription

func (c computerPlaces) DescribeComputer(_ context.Context, id string) (command.ComputerDescription, error) {
	return c[id], nil
}

func environmentClient(t *testing.T, remote *environmentServer, control *dispatcherServer) *execution.Environment {
	t.Helper()
	client, err := execution.NewEnvironment(remote, controlClient(t, control), &storageMounts{}, computerPlaces{})
	require.NoError(t, err)
	return client
}

func TestSessionEnvironmentIntegrationPreservesSnapshotContinuityAndCompletionAuthority(t *testing.T) {
	remote := &environmentServer{calls: make(chan resourceCall, 10)}
	control := &dispatcherServer{}
	port := environmentClient(t, remote, control)
	capabilities := domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, Prompt: "fixed prompt\n", StartupAgent: "reviewer:architect", Plugins: []domain.SelectedPlugin{{Release: domain.PluginRelease{Slug: "reviewer", Source: "owned", Version: "1", RetrievalRef: "archive:reviewer", NativeEntry: []byte(`{"name":"reviewer","version":"1"}`), Agents: []domain.AgentComponent{{Name: "reviewer:architect"}}, Parameters: pluginconfig.ParameterDeclarations{"language": {Type: "string", Title: "Language", Description: "Language"}}}, Configuration: pluginconfig.Configuration{"language": "zh"}}}}
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{RequiredPlugins: []string{"reviewer"}}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.Must(uuid.NewV7()).String(), AgentID: agent.ID, Capabilities: capabilities, WorkspaceReference: "authorized-workspace", HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, port)
	require.NoError(t, err)
	ctx := context.Background()
	originalSession := session.ID
	var previousContext string
	for i, begin := range []func(context.Context, domain.StartAdmission, domain.ExecutionPort) error{nil, session.Resume, session.Restart} {
		stopID := []string{"stop-first", "stop-resumed", "stop-restarted"}[i]
		if begin != nil {
			current := make([]domain.PluginRelease, 0, len(session.CapabilitySnapshot.Plugins))
			for _, plugin := range session.CapabilitySnapshot.Plugins {
				current = append(current, plugin.Release)
			}
			require.NoError(t, begin(ctx, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true, CurrentPlugins: current}, port))
		}
		require.Equal(t, domain.WorkStarting, session.State)
		call := <-remote.calls
		require.Equal(t, session.ID, call.create.WorkID)
		require.Equal(t, capabilities.Prompt, call.create.Capabilities.Prompt)
		require.Equal(t, "reviewer:architect", call.create.Capabilities.StartupAgent)
		require.Equal(t, "reviewer", call.create.Capabilities.Plugins[0].Release.Slug)
		require.Equal(t, pluginconfig.Configuration{"language": "zh"}, call.create.Capabilities.Plugins[0].Configuration)
		require.Equal(t, "authorized-workspace", call.create.WorkspaceReference)
		require.Equal(t, "workspaces/authorized-workspace", call.mounts.Workspace.SubPath, "every start mounts the Work's one workspace")
		require.Equal(t, "homes/user-home", call.mounts.Home.SubPath)
		require.Equal(t, "user-home", call.create.HomeBinding.OwnerID)
		require.Equal(t, "project", call.create.HomeBinding.Namespace)
		require.Equal(t, session.HarnessSessionReference, call.create.HarnessSessionReference)
		if i == 1 {
			require.Equal(t, previousContext, call.create.HarnessSessionReference)
			require.Equal(t, domain.ContextContinue, call.create.ContextMode)
		} else if i == 2 {
			require.Equal(t, previousContext, call.create.HarnessSessionReference)
			require.Equal(t, domain.ContextRecover, call.create.ContextMode)
		} else {
			require.NotEqual(t, previousContext, call.create.HarnessSessionReference)
			require.Equal(t, domain.ContextFresh, call.create.ContextMode)
		}
		previousContext = session.HarnessSessionReference
		require.Equal(t, originalSession, session.ID)
		require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
		require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
		require.NoError(t, session.Stop(ctx, stopID, port))
		require.Equal(t, domain.WorkStopping, session.State)
		require.Empty(t, remote.calls, "Stop only requests Harness exit, not compute deletion")
		controlCalls := control.observed()
		require.Equal(t, stopID, controlCalls[len(controlCalls)-1].stopID)
		if i == 1 {
			require.NoError(t, session.RecordFailure("failed during stop"))
		}
		require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, port))
		require.Equal(t, session.ID, (<-remote.calls).deleted)
		require.Equal(t, i == 1, session.State == domain.WorkFailed, "delete acceptance cannot confirm removal")
		require.NoError(t, session.ConfirmComputeReclaimed())
		if i == 1 {
			require.Equal(t, domain.WorkFailed, session.State)
		} else {
			require.Equal(t, domain.WorkStopped, session.State)
		}
	}
}

func TestEnvironmentFailureDoesNotRetryOrChangeTheRequest(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeResourceExhausted, connect.CodeUnavailable} {
		t.Run(code.String(), func(t *testing.T) {
			remote := &environmentServer{calls: make(chan resourceCall, 4), failure: connect.NewError(code, errors.New("allocation unavailable"))}
			port := environmentClient(t, remote, &dispatcherServer{})
			request := domain.StartExecutionRequest{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, WorkID: "work", HarnessSessionReference: "context", ContextMode: domain.ContextContinue, WorkspaceReference: "workspace"}
			err := port.StartExecution(context.Background(), request)
			require.ErrorIs(t, err, remote.failure)
			require.Equal(t, code, connect.CodeOf(err))
			call := <-remote.calls
			require.Equal(t, "work", call.create.WorkID)
			require.Equal(t, domain.ContextContinue, call.create.ContextMode)
			require.Empty(t, remote.calls)
			err = port.ReclaimCompute(context.Background(), "work")
			require.ErrorIs(t, err, remote.failure)
			require.Equal(t, "work", (<-remote.calls).deleted)
			require.Empty(t, remote.calls)
			err = port.RetireFencedRun(context.Background(), "work")
			require.ErrorIs(t, err, remote.failure)
			require.Equal(t, resourceCall{retired: "work"}, <-remote.calls, "a fenced run retires without reclaiming compute")
			require.Empty(t, remote.calls)
		})
	}
}

func TestEnvironmentRejectsIncompleteTranslationAndPreservesCancellation(t *testing.T) {
	remote := &environmentServer{calls: make(chan resourceCall, 4)}
	port := environmentClient(t, remote, &dispatcherServer{})
	request := domain.StartExecutionRequest{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, WorkID: "work", HarnessSessionReference: "context", WorkspaceReference: "workspace", ContextMode: domain.ContextContinue}
	invalid := request
	invalid.WorkID = ""
	require.ErrorIs(t, port.StartExecution(context.Background(), invalid), domain.ErrExecutionRequest)
	invalid = request
	invalid.ContextMode = "unexpected"
	require.ErrorIs(t, port.StartExecution(context.Background(), invalid), domain.ErrExecutionRequest)
	invalid = request
	invalid.HomeBinding = domain.HomeBinding{OwnerID: "user-home"}
	require.ErrorIs(t, port.StartExecution(context.Background(), invalid), domain.ErrExecutionRequest)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, port.StartExecution(ctx, request), context.Canceled)
	require.ErrorIs(t, port.ReclaimCompute(ctx, request.WorkID), context.Canceled)
	require.Empty(t, remote.calls)
}

// A start that never launched a Harness process has nothing Dispatcher can
// stop: the stop settles without asking Dispatcher, and observation reports
// the abandonment.
func TestStopOfAnAbandonedStartAsksNoDispatcher(t *testing.T) {
	control := &dispatcherServer{}
	port := environmentClient(t, &environmentServer{calls: make(chan resourceCall, 1), abandoned: true}, control)
	require.NoError(t, port.StopExecution(context.Background(), "work", "stop"))
	control.mu.Lock()
	defer control.mu.Unlock()
	require.Empty(t, control.calls, "Dispatcher is not asked")
}

// The execution port routes each start by its Work's Computer: a Managed
// Computer's start acquires no mounts, and Resume and Restart start on the
// same Computer.
func TestEnvironmentRoutesAStartByItsComputersKind(t *testing.T) {
	const managed = "01900000-0000-7000-8000-0000000000c1"
	remote := &environmentServer{calls: make(chan resourceCall, 4)}
	mounts := &storageMounts{}
	port, err := execution.NewEnvironment(remote, controlClient(t, &dispatcherServer{}), mounts, computerPlaces{managed: {Managed: true}})
	require.NoError(t, err)
	input := domain.StartExecutionRequest{WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, WorkID: "work", ComputerID: managed, Capabilities: domain.CapabilitySnapshot{Prompt: "prompt", Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, HarnessSessionReference: "context", ContextMode: domain.ContextFresh, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "employee"}}
	require.NoError(t, port.StartExecution(context.Background(), input))
	call := <-remote.calls
	require.True(t, call.managed)
	require.Equal(t, managed, call.create.ComputerID)
	require.Empty(t, mounts.requests, "a Work on a Managed Computer acquires no mounts")

	input.ComputerID = "01a19bc3-075a-7c57-b42b-7fdf73b611ed"
	require.NoError(t, port.StartExecution(context.Background(), input))
	call = <-remote.calls
	require.False(t, call.managed)
	require.Len(t, mounts.requests, 1, "a Cloud start is created with the mounts Storage grants")
}
