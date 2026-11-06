//go:build integration

package agent_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/talgent-work/workbench/pkg/mysqltest"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"
)

// Exercise initialization through the production module and private HTTP API.
// The real schema and a fresh connection on restart own all persistence. Native
// execution delivery and external Kubernetes quota are controlled; definition
// initialization and Session registration/admission use production handlers.
func TestDefinitionInitializationThroughPrivateAPIAndModuleRestart(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	port := &sessionCommandRecorder{}
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	startModule := func() (agentprivatev1connect.AgentServiceClient, func()) {
		t.Helper()
		db, err := xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		mux := http.NewServeMux()
		app := fx.New(agent.Module, sessionStateTransport(t), moduleCatalog(), fx.NopLogger, storageStub(),
			fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
			fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
			fx.Provide(func() kubernetes.Interface { return fake.NewClientset(quota.DeepCopy()) }),
			fx.Provide(func() domain.ExecutionPort { return port }),
			fx.Provide(func() command.InputBufferPort { return port }),
		)
		require.NoError(t, app.Err())
		require.NoError(t, app.Start(ctx))
		server := httptest.NewServer(mux)
		var once sync.Once
		stop := func() {
			once.Do(func() {
				server.Close()
				require.NoError(t, app.Stop(ctx))
				require.NoError(t, db.Close())
			})
		}
		t.Cleanup(stop)
		return agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL), stop
	}
	client, stop := startModule()
	// A caller ensures a Project's own definition with its own plugin and
	// startup role, owned by the Project's Owner and public within the Project.
	ensureProjectDefinition := func(projectID, purpose, owner string) (*agentprivatev1.EnsureAgentDefinitionResponse, error) {
		response, err := client.EnsureAgentDefinition(ctx, sessionControlRequest(&agentprivatev1.EnsureAgentDefinitionRequest{
			Owners:         []string{owner},
			UsageScope:     &agentprivatev1.UsageScope{Kind: "project", Reference: projectID},
			Purpose:        purpose,
			HomeBinding:    &agentprivatev1.HomeBinding{OwnerId: "project/" + projectID + "/" + purpose, Namespace: purpose},
			Capabilities:   &agentprivatev1.Capabilities{RequiredPlugins: []string{"talgent-runtime", "caller-plugin"}, StartupAgent: "caller-plugin:coordinator"},
			VisibilityRule: &agentprivatev1.VisibilityRule{Public: true},
		}))
		if err != nil {
			return nil, err
		}
		return response.Msg, nil
	}
	// A capability query offers the product defaults of an employee
	// definition not yet established and never creates it.
	optionsRequest := connect.NewRequest(&agentprivatev1.GetWorkStartOptionsRequest{SubjectId: "caller", EmployeeId: "caller"})
	_, err = client.GetWorkStartOptions(ctx, optionsRequest)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	optionsRequest.Header().Set("Authorization", "Bearer control")
	options, err := client.GetWorkStartOptions(ctx, optionsRequest)
	require.NoError(t, err)
	require.Equal(t, "talgent-runtime:personal-work-executor", options.Msg.DefaultStartupAgent)
	require.Equal(t, "talgent-runtime", options.Msg.Plugins[len(options.Msg.Plugins)-1].Name)
	_, err = client.GetAgentDefinitionForPurpose(ctx, sessionControlRequest(&agentprivatev1.GetAgentDefinitionForPurposeRequest{UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "caller"}, Purpose: "work"}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "capability discovery cannot initialize the definition")
	_, err = client.EnsureAgentDefinition(ctx, connect.NewRequest(&agentprivatev1.EnsureAgentDefinitionRequest{Owners: []string{"owner"}, UsageScope: &agentprivatev1.UsageScope{Kind: "project", Reference: "fresh-project"}, Purpose: "project_coordination"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	selections := []*agentprivatev1.GetAgentDefinitionForPurposeRequest{
		{UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "alice"}, Purpose: "work"},
		{UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "bob"}, Purpose: "work"},
		{UsageScope: &agentprivatev1.UsageScope{Kind: "project", Reference: "project-a"}, Purpose: "project_coordination"},
		{UsageScope: &agentprivatev1.UsageScope{Kind: "project", Reference: "project-a"}, Purpose: "caller-defined"},
	}
	ids := make([]string, len(selections))
	homes := make([]*agentprivatev1.HomeBinding, len(selections))
	for i, selection := range selections {
		homes[i] = &agentprivatev1.HomeBinding{OwnerId: selection.UsageScope.Reference + "/" + selection.Purpose, Namespace: "context"}
		// A person owns its own Agent; the Project's Owner owns the others.
		owners := []string{"owner"}
		if selection.UsageScope.Kind == "employee" {
			owners = []string{selection.UsageScope.Reference}
		}
		input := &agentprivatev1.CreateAgentDefinitionRequest{Owners: owners, AgentName: "Fixture Agent", UsageScope: selection.UsageScope, Purpose: selection.Purpose, HomeBinding: homes[i],
			Capabilities: &agentprivatev1.Capabilities{Prompt: "Follow the authorized task."},
		}
		_, err = client.CreateAgentDefinition(ctx, connect.NewRequest(input))
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		request := connect.NewRequest(input)
		request.Header().Set("Authorization", "Bearer control")
		created, err := client.CreateAgentDefinition(ctx, request)
		require.NoError(t, err, "unauthorized creation must not reserve this definition")
		ids[i] = created.Msg.AgentId
		require.NotEmpty(t, ids[i])
		require.NotContains(t, ids[:i], ids[i])
		_, err = client.CreateAgentDefinition(ctx, request)
		require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "initialization cannot create another definition for the same purpose")
	}

	legacy, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	_, err = legacy.Exec("UPDATE agent_definition SET agent_name = '' WHERE scope_ref IN ('alice','bob','project-a')")
	require.NoError(t, err)
	require.NoError(t, legacy.Close())
	// Existing definitions gain names without replacing their custom HOME,
	// capabilities, concurrency, or association rules.
	names := make([]string, len(selections))
	for i, selection := range selections {
		request := connect.NewRequest(&agentprivatev1.EnsureAgentDefinitionRequest{Owners: []string{"owner"}, UsageScope: selection.UsageScope, Purpose: selection.Purpose, EmployeeDisplayName: "张三 Jack"})
		request.Header().Set("Authorization", "Bearer control")
		result, err := client.EnsureAgentDefinition(ctx, request)
		require.NoError(t, err)
		require.Equal(t, ids[i], result.Msg.AgentId)
		require.NotEmpty(t, result.Msg.AgentName)
		names[i] = result.Msg.AgentName
		if selection.UsageScope.Kind == "employee" {
			require.Equal(t, "张三 Jack's Agent", names[i])
		}
	}
	createSession := connect.NewRequest(&agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), AgentId: ids[0], SubjectId: "alice", WorkspaceReference: "employee-workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "case-a"}, ProjectId: "project-a"})
	createSession.Header().Set("Authorization", "Bearer control")
	createdSession, err := client.CreateWork(ctx, createSession)
	require.NoError(t, err)
	readSession := connect.NewRequest(&agentprivatev1.GetWorkRequest{SubjectId: "alice", WorkId: createdSession.Msg.WorkId})
	readSession.Header().Set("Authorization", "Bearer control")
	before, err := client.GetWork(ctx, readSession)
	require.NoError(t, err)
	require.Equal(t, "张三 Jack's Agent", before.Msg.AgentName)
	// A changed display name, supplied again as every Work start does, keeps
	// the Work Agent's established name.
	again := connect.NewRequest(&agentprivatev1.EnsureAgentDefinitionRequest{Owners: []string{"owner"}, UsageScope: selections[0].UsageScope, Purpose: selections[0].Purpose, EmployeeDisplayName: "李四 Jane"})
	again.Header().Set("Authorization", "Bearer control")
	_, err = client.EnsureAgentDefinition(ctx, again)
	require.NoError(t, err)
	after, err := client.GetWork(ctx, readSession)
	require.NoError(t, err)
	require.Equal(t, before.Msg.WorkId, after.Msg.WorkId)
	require.Equal(t, "张三 Jack's Agent", after.Msg.AgentName)
	// A real unique constraint resolves concurrent initialization from distinct
	// requests; no fake repository serializes the contenders.
	const contenders = 12
	// The Project's own definition is owned by the Project's Owner.
	const projectOwner = "project-owner"
	type outcome struct {
		id, name string
		err      error
	}
	outcomes := make(chan outcome, contenders)
	gate := make(chan struct{})
	for range contenders {
		go func() {
			<-gate
			result, err := ensureProjectDefinition("fresh-project", "project_coordination", projectOwner)
			if err != nil {
				outcomes <- outcome{err: err}
				return
			}
			outcomes <- outcome{id: result.AgentId, name: result.AgentName}
		}()
	}
	close(gate)
	var concurrentID, concurrentName string
	for range contenders {
		result := <-outcomes
		require.NoError(t, result.err)
		if concurrentID == "" {
			concurrentID, concurrentName = result.id, result.name
		}
		require.Equal(t, concurrentID, result.id)
		require.Equal(t, concurrentName, result.name)
	}
	fresh := map[string]*agentprivatev1.GetAgentDefinitionResponse{}
	for _, purpose := range []string{"project_coordination"} {
		result, err := ensureProjectDefinition("fresh-project", purpose, projectOwner)
		require.NoError(t, err)
		get := connect.NewRequest(&agentprivatev1.GetAgentDefinitionRequest{AgentId: result.AgentId})
		get.Header().Set("Authorization", "Bearer control")
		definition, err := client.GetAgentDefinition(ctx, get)
		require.NoError(t, err)
		fresh[purpose] = definition.Msg
	}
	require.Equal(t, "caller-plugin:coordinator", fresh["project_coordination"].Capabilities.StartupAgent)
	require.Equal(t, []string{"talgent-runtime", "caller-plugin"}, fresh["project_coordination"].Capabilities.RequiredPlugins, "the Project's Agent runs the caller's plugin beside the core one")
	require.Equal(t, []string{projectOwner}, fresh["project_coordination"].Owners)
	// A project-scoped definition's Works are started on its Owner's
	// authority; the same Subject locates the same durable Work across a
	// restart, and the Project Work has no Session Workspace.
	locateProjectWork := func(startedBy string) (*connect.Response[agentprivatev1.CreateWorkResponse], error) {
		t.Helper()
		id := fresh["project_coordination"].AgentId
		return client.CreateWork(ctx, sessionControlRequest(&agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: startedBy, AgentId: id, Subject: &agentprivatev1.WorkSubject{Kind: "project", Id: "fresh-project"}, ProjectId: "fresh-project"}))
	}
	located, err := locateProjectWork(projectOwner)
	require.NoError(t, err)
	projectWorkID := located.Msg.WorkId
	require.Len(t, port.startRequests(), 2, "employee and project Work creation each admit initial execution")
	stop()
	client, _ = startModule()
	located, err = locateProjectWork(projectOwner)
	require.NoError(t, err)
	require.Equal(t, projectWorkID, located.Msg.WorkId)
	for _, prior := range fresh {
		get := connect.NewRequest(&agentprivatev1.GetAgentDefinitionRequest{AgentId: prior.AgentId})
		get.Header().Set("Authorization", "Bearer control")
		current, err := client.GetAgentDefinition(ctx, get)
		require.NoError(t, err)
		require.True(t, proto.Equal(prior, current.Msg), "restart preserves names and defaults")
	}
	// Transferring the Project's ownership hands every Work of its definition
	// to the new Owner; the former Owner no longer starts them.
	_, err = client.TransferAgentDefinitionOwners(ctx, sessionControlRequest(&agentprivatev1.TransferAgentDefinitionOwnersRequest{AgentId: fresh["project_coordination"].AgentId, Owners: []string{"next-owner"}}))
	require.NoError(t, err)
	_, err = locateProjectWork(projectOwner)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	located, err = locateProjectWork("next-owner")
	require.NoError(t, err)
	require.Equal(t, projectWorkID, located.Msg.WorkId)

	for i, selection := range selections {
		request := connect.NewRequest(selection)
		request.Header().Set("Authorization", "Bearer control")
		resolved, err := client.GetAgentDefinitionForPurpose(ctx, request)
		require.NoError(t, err)
		require.Equal(t, ids[i], resolved.Msg.AgentId)
		get := connect.NewRequest(&agentprivatev1.GetAgentDefinitionRequest{AgentId: resolved.Msg.AgentId})
		get.Header().Set("Authorization", "Bearer control")
		definition, err := client.GetAgentDefinition(ctx, get)
		require.NoError(t, err)
		require.Equal(t, selection.Purpose, definition.Msg.Purpose)
		require.Equal(t, names[i], definition.Msg.AgentName)
		require.True(t, proto.Equal(selection.UsageScope, definition.Msg.UsageScope))
		require.True(t, proto.Equal(homes[i], definition.Msg.HomeBinding))
		require.Equal(t, "Follow the authorized task.", definition.Msg.Capabilities.Prompt)
	}
	observer, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	rows, err := observer.QueryString("SELECT COUNT(*) AS total FROM agent_definition WHERE scope_kind='project' AND scope_ref='fresh-project'")
	require.NoError(t, err)
	require.Equal(t, "1", rows[0]["total"], "concurrent initialization creates one definition per purpose")
	rows, err = observer.QueryString("SELECT COUNT(*) AS total FROM agent_session WHERE agent_id = ?", fresh["project_coordination"].AgentId)
	require.NoError(t, err)
	require.Equal(t, "1", rows[0]["total"], "reuse after restart creates no duplicate Work")
	require.NoError(t, observer.Close())
	missingEmployee := connect.NewRequest(&agentprivatev1.GetAgentDefinitionForPurposeRequest{UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "never-started"}, Purpose: "work"})
	missingEmployee.Header().Set("Authorization", "Bearer control")
	_, err = client.GetAgentDefinitionForPurpose(ctx, missingEmployee)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "reads do not initialize unused employee definitions")
	missing := connect.NewRequest(&agentprivatev1.GetAgentDefinitionForPurposeRequest{UsageScope: &agentprivatev1.UsageScope{Kind: "project", Reference: "project-b"}, Purpose: "caller-defined"})
	missing.Header().Set("Authorization", "Bearer control")
	_, err = client.GetAgentDefinitionForPurpose(ctx, missing)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another project cannot supply the missing definition")
}
