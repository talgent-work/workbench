//go:build integration

package agent_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	"github.com/talgent-work/contracts/gen/agent/private/v1/agentprivatev1connect"
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
		return agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0)))), stop
	}
	client, stop := startModule()
	// A caller ensures a Project's own definition with its own plugin and
	// startup role, owned by the Project's Owner and public within the Project.
	ensureProjectDefinition := func(projectID, purpose, owner string) (*agentprivatev1.EnsureAgentDefinitionResponse, error) {
		response, err := client.EnsureAgentDefinition(sessionControlRequest(ctx), &agentprivatev1.EnsureAgentDefinitionRequest{
			Owners:         []string{owner},
			UsageScope:     &agentprivatev1.UsageScope{Kind: "project", Reference: projectID},
			Purpose:        purpose,
			HomeBinding:    &agentprivatev1.HomeBinding{OwnerId: "project/" + projectID + "/" + purpose, Namespace: purpose},
			Capabilities:   &agentprivatev1.Capabilities{RequiredPlugins: []string{"talgent-runtime", "caller-plugin"}, StartupAgent: "caller-plugin:coordinator"},
			VisibilityRule: &agentprivatev1.VisibilityRule{Public: true},
		})
		if err != nil {
			return nil, err
		}
		return response, nil
	}
	// A capability query offers the product defaults of an employee
	// definition not yet established and never creates it.
	optionsRequest := &agentprivatev1.GetWorkStartOptionsRequest{SubjectId: "caller", EmployeeId: "caller"}
	_, err = client.GetWorkStartOptions(ctx, optionsRequest)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	optionsRequestCtx, optionsRequestInfo := connect.NewClientContext(ctx)
	optionsRequestInfo.RequestHeader().Set("Authorization", "Bearer control")
	options, err := client.GetWorkStartOptions(optionsRequestCtx, optionsRequest)
	require.NoError(t, err)
	require.Equal(t, "talgent-runtime:personal-work-executor", options.DefaultStartupAgent)
	require.Equal(t, "talgent-runtime", options.Plugins[len(options.Plugins)-1].Name)
	_, err = client.GetAgentDefinitionForPurpose(sessionControlRequest(ctx), &agentprivatev1.GetAgentDefinitionForPurposeRequest{UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "caller"}, Purpose: "work"})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "capability discovery cannot initialize the definition")
	_, err = client.EnsureAgentDefinition(ctx, &agentprivatev1.EnsureAgentDefinitionRequest{Owners: []string{"owner"}, UsageScope: &agentprivatev1.UsageScope{Kind: "project", Reference: "fresh-project"}, Purpose: "project_coordination"})
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
		_, err = client.CreateAgentDefinition(ctx, input)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		request := input
		requestCtx, requestInfo := connect.NewClientContext(ctx)
		requestInfo.RequestHeader().Set("Authorization", "Bearer control")
		created, err := client.CreateAgentDefinition(requestCtx, request)
		require.NoError(t, err, "unauthorized creation must not reserve this definition")
		ids[i] = created.AgentId
		require.NotEmpty(t, ids[i])
		require.NotContains(t, ids[:i], ids[i])
		_, err = client.CreateAgentDefinition(requestCtx, request)
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
		request := &agentprivatev1.EnsureAgentDefinitionRequest{Owners: []string{"owner"}, UsageScope: selection.UsageScope, Purpose: selection.Purpose, EmployeeDisplayName: "张三 Jack"}
		requestCtx, requestInfo := connect.NewClientContext(ctx)
		requestInfo.RequestHeader().Set("Authorization", "Bearer control")
		result, err := client.EnsureAgentDefinition(requestCtx, request)
		require.NoError(t, err)
		require.Equal(t, ids[i], result.AgentId)
		require.NotEmpty(t, result.AgentName)
		names[i] = result.AgentName
		if selection.UsageScope.Kind == "employee" {
			require.Equal(t, "张三 Jack's Agent", names[i])
		}
	}
	createSession := &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), AgentId: ids[0], SubjectId: "alice", WorkspaceReference: "employee-workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "case-a"}, ProjectId: "project-a"}
	createSessionCtx, createSessionInfo := connect.NewClientContext(ctx)
	createSessionInfo.RequestHeader().Set("Authorization", "Bearer control")
	createdSession, err := client.CreateWork(createSessionCtx, createSession)
	require.NoError(t, err)
	readSession := &agentprivatev1.GetWorkRequest{SubjectId: "alice", WorkId: createdSession.WorkId}
	readSessionCtx, readSessionInfo := connect.NewClientContext(ctx)
	readSessionInfo.RequestHeader().Set("Authorization", "Bearer control")
	before, err := client.GetWork(readSessionCtx, readSession)
	require.NoError(t, err)
	require.Equal(t, "张三 Jack's Agent", before.AgentName)
	// A changed display name, supplied again as every Work start does, keeps
	// the Work Agent's established name.
	again := &agentprivatev1.EnsureAgentDefinitionRequest{Owners: []string{"owner"}, UsageScope: selections[0].UsageScope, Purpose: selections[0].Purpose, EmployeeDisplayName: "李四 Jane"}
	againCtx, againInfo := connect.NewClientContext(ctx)
	againInfo.RequestHeader().Set("Authorization", "Bearer control")
	_, err = client.EnsureAgentDefinition(againCtx, again)
	require.NoError(t, err)
	after, err := client.GetWork(readSessionCtx, readSession)
	require.NoError(t, err)
	require.Equal(t, before.WorkId, after.WorkId)
	require.Equal(t, "张三 Jack's Agent", after.AgentName)
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
		get := &agentprivatev1.GetAgentDefinitionRequest{AgentId: result.AgentId}
		getCtx, getInfo := connect.NewClientContext(ctx)
		getInfo.RequestHeader().Set("Authorization", "Bearer control")
		definition, err := client.GetAgentDefinition(getCtx, get)
		require.NoError(t, err)
		fresh[purpose] = definition
	}
	require.Equal(t, "caller-plugin:coordinator", fresh["project_coordination"].Capabilities.StartupAgent)
	require.Equal(t, []string{"talgent-runtime", "caller-plugin"}, fresh["project_coordination"].Capabilities.RequiredPlugins, "the Project's Agent runs the caller's plugin beside the core one")
	require.Equal(t, []string{projectOwner}, fresh["project_coordination"].Owners)
	// A project-scoped definition's Works are started on its Owner's
	// authority; the same Subject locates the same durable Work across a
	// restart, and the Project Work has no Session Workspace.
	locateProjectWork := func(startedBy string) (*agentprivatev1.CreateWorkResponse, error) {
		t.Helper()
		id := fresh["project_coordination"].AgentId
		return client.CreateWork(sessionControlRequest(ctx), &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: startedBy, AgentId: id, Subject: &agentprivatev1.WorkSubject{Kind: "project", Id: "fresh-project"}, ProjectId: "fresh-project"})
	}
	located, err := locateProjectWork(projectOwner)
	require.NoError(t, err)
	projectWorkID := located.WorkId
	require.Len(t, port.startRequests(), 2, "employee and project Work creation each admit initial execution")
	stop()
	client, _ = startModule()
	located, err = locateProjectWork(projectOwner)
	require.NoError(t, err)
	require.Equal(t, projectWorkID, located.WorkId)
	for _, prior := range fresh {
		get := &agentprivatev1.GetAgentDefinitionRequest{AgentId: prior.AgentId}
		getCtx, getInfo := connect.NewClientContext(ctx)
		getInfo.RequestHeader().Set("Authorization", "Bearer control")
		current, err := client.GetAgentDefinition(getCtx, get)
		require.NoError(t, err)
		require.True(t, proto.Equal(prior, current), "restart preserves names and defaults")
	}
	// Transferring the Project's ownership hands every Work of its definition
	// to the new Owner; the former Owner no longer starts them.
	_, err = client.TransferAgentDefinitionOwners(sessionControlRequest(ctx), &agentprivatev1.TransferAgentDefinitionOwnersRequest{AgentId: fresh["project_coordination"].AgentId, Owners: []string{"next-owner"}})
	require.NoError(t, err)
	_, err = locateProjectWork(projectOwner)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	located, err = locateProjectWork("next-owner")
	require.NoError(t, err)
	require.Equal(t, projectWorkID, located.WorkId)

	for i, selection := range selections {
		request := selection
		requestCtx, requestInfo := connect.NewClientContext(ctx)
		requestInfo.RequestHeader().Set("Authorization", "Bearer control")
		resolved, err := client.GetAgentDefinitionForPurpose(requestCtx, request)
		require.NoError(t, err)
		require.Equal(t, ids[i], resolved.AgentId)
		get := &agentprivatev1.GetAgentDefinitionRequest{AgentId: resolved.AgentId}
		getCtx, getInfo := connect.NewClientContext(ctx)
		getInfo.RequestHeader().Set("Authorization", "Bearer control")
		definition, err := client.GetAgentDefinition(getCtx, get)
		require.NoError(t, err)
		require.Equal(t, selection.Purpose, definition.Purpose)
		require.Equal(t, names[i], definition.AgentName)
		require.True(t, proto.Equal(selection.UsageScope, definition.UsageScope))
		require.True(t, proto.Equal(homes[i], definition.HomeBinding))
		require.Equal(t, "Follow the authorized task.", definition.Capabilities.Prompt)
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
	missingEmployee := &agentprivatev1.GetAgentDefinitionForPurposeRequest{UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "never-started"}, Purpose: "work"}
	missingEmployeeCtx, missingEmployeeInfo := connect.NewClientContext(ctx)
	missingEmployeeInfo.RequestHeader().Set("Authorization", "Bearer control")
	_, err = client.GetAgentDefinitionForPurpose(missingEmployeeCtx, missingEmployee)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "reads do not initialize unused employee definitions")
	missing := &agentprivatev1.GetAgentDefinitionForPurposeRequest{UsageScope: &agentprivatev1.UsageScope{Kind: "project", Reference: "project-b"}, Purpose: "caller-defined"}
	missingCtx, missingInfo := connect.NewClientContext(ctx)
	missingInfo.RequestHeader().Set("Authorization", "Bearer control")
	_, err = client.GetAgentDefinitionForPurpose(missingCtx, missing)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another project cannot supply the missing definition")
}
