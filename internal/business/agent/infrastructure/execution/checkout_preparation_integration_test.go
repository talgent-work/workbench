//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"
)

// The start's preparation process receives the first start's checkout plan
// and its report ends the checkout step before the Harness starts: each
// planned repository is recorded checked out or not, with its reason.
func TestPreparationReportRecordsTheFirstStartsCheckouts(t *testing.T) {
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	initial := infrastructure.NewInitialExecutionRequests(db, time.Now)
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Fixture Agent", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, definitions.Save(ctx, definition))
	definition, err = definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	work, err := domain.NewAgentWork(ctx, domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: "workspace", HomeBinding: definition.HomeBinding, Capabilities: domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors}}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, executionDiscard{})
	require.NoError(t, err)
	input := command.PrepareWorkExecution{WorkID: work.ID, SubjectID: "owner", WorkspaceReference: work.WorkspaceReference, RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-396", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "work on the repositories"}}
	require.NoError(t, works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "owner", DefinitionVersion: definition.Version, RequestID: input.RequestID, InitialExecution: &input}))
	retained, err := initial.Get(ctx, work.ID)
	require.NoError(t, err)
	retained.Prepared = true
	retained.Checkouts = []command.RepositoryCheckout{
		{RepoID: "github:1", CloneURL: "https://github.com/acme/api.git", BaseBranch: "main", WorkBranch: "talgent/TAL-396", TargetPath: "/workspace/repos/api"},
		{RepoID: "github:2", CloneURL: "https://github.com/acme/private.git", BaseBranch: "main", WorkBranch: "talgent/TAL-396", TargetPath: "/workspace/repos/private"},
	}
	require.NoError(t, initial.Save(ctx, retained))

	record := allocationRecord{ID: uuid.NewString(), SessionID: work.ID, SandboxID: "sandbox", Version: 1, PreparationCommand: "prepare", RequestJSON: []byte(`{}`), SpecificationJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`)}
	_, err = db.Insert(&record)
	require.NoError(t, err)
	var created string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pty", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Cwd, Command string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		created = body.Command
		_, _ = w.Write([]byte(`{"session_id":"prepare-1"}`))
	})
	mux.Handle("GET /pty/prepare-1/ws", websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		_ = websocket.Message.Send(ws, `{"type":"connected","session_id":"prepare-1","mode":"pipe","role":"holder"}`)
		report := `{"results":[],"repositories":[{"repositoryId":"github:1","targetPath":"/workspace/repos/api","checkedOut":true,"reason":""},{"repositoryId":"github:2","targetPath":"/workspace/repos/private","checkedOut":false,"reason":"checkout_unavailable"}]}` + "\n"
		_ = websocket.Message.Send(ws, append([]byte{1}, []byte(report)...))
		_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":0}`)
	}))
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "fixture-token")
	require.NoError(t, err)
	// Controlplane takes no part in checkout beyond sending the plan: this
	// Provisioning has no storage root, so it cannot write the workspace.
	n := &NativeConnections{provisioning: &Provisioning{db: db, now: time.Now, initial: initial}, checkouts: command.NewRecordRepositoryCheckoutsHandler(initial)}
	require.NoError(t, n.prepareEnvironment(ctx, &record, client, domain.CapabilitySnapshot{}))
	require.True(t, strings.HasPrefix(created, "talgent_checkout_plan='"), "the process receives the plan: %s", created)
	for _, secret := range []string{"Authorization", "x-access-token", "credential.helper=!", "GIT_ASKPASS"} {
		require.NotContains(t, created, secret, "the plan carries no credential; the Credential Proxy adds it")
	}
	require.Contains(t, created, `"targetPath":"/workspace/repos/private"`)
	require.True(t, record.EnvironmentPrepared)
	stored, found, err := latestAllocation(ctx, db, work.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.JSONEq(t, `[{"repositoryId":"github:1","cloneUrl":"https://github.com/acme/api.git","baseBranch":"main","workBranch":"talgent/TAL-396","targetPath":"/workspace/repos/api"},{"repositoryId":"github:2","cloneUrl":"https://github.com/acme/private.git","baseBranch":"main","workBranch":"talgent/TAL-396","targetPath":"/workspace/repos/private"}]`, string(stored.CheckoutPlan))
	outcome, err := initial.Get(ctx, work.ID)
	require.NoError(t, err)
	require.True(t, outcome.InputPrepared, "checkout ended before the Harness starts")
	require.Equal(t, []command.RepositoryCheckoutResult{
		{RepositoryID: "github:1", TargetPath: "/workspace/repos/api", Prepared: true},
		{RepositoryID: "github:2", TargetPath: "/workspace/repos/private", Reason: command.CheckoutUnavailable},
	}, outcome.CheckoutResults)
	plan, err := n.provisioning.checkoutPlan(ctx, work.ID)
	require.NoError(t, err)
	require.Nil(t, plan, "Resume and Restart check nothing out again")
}

// executionDiscard accepts the start of a Work registered directly here.
type executionDiscard struct{}

func (executionDiscard) StartExecution(context.Context, domain.StartExecutionRequest) error {
	return nil
}
func (executionDiscard) StopExecution(context.Context, string, string) error { return nil }
func (executionDiscard) ReclaimCompute(context.Context, string) error        { return nil }
func (executionDiscard) RetireFencedRun(context.Context, string) error       { return nil }

func (executionDiscard) CancelRunningTurn(context.Context, string) error { return nil }
