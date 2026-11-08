//go:build integration

package execution

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// checkoutSelection selects acme/api and acme/private on the work branch
// talgent/TAL-396.
func checkoutSelection() domain.WorkRepositorySelection {
	return domain.WorkRepositorySelection{OwnerUserID: "owner", WorkBranch: "talgent/TAL-396", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "github:1", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "main", BaseBranch: "main"},
		{Provider: "github", RepoID: "github:2", RepoName: "acme/private", CloneURL: "https://github.com/acme/private.git", DefaultBranch: "main", BaseBranch: "main"},
	}}
}

// preparationServer is an execd fixture whose preparation process writes
// report and exits with exit; created receives the command it was given.
func preparationServer(t *testing.T, report string, exit int) (*execd.Client, *string) {
	t.Helper()
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
		if report != "" {
			_ = websocket.Message.Send(ws, append([]byte{1}, []byte(report+"\n")...))
		}
		_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":`+strconv.Itoa(exit)+`}`)
	}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := execd.NewClient(server.Client(), server.URL, "fixture-token")
	require.NoError(t, err)
	return client, &created
}

// preparedRecord inserts the live execution record of a start launched with
// launch.
func preparedRecord(t *testing.T, db *xorm.Engine, launch domain.LaunchSpecification) allocationRecord {
	t.Helper()
	encoded, err := json.Marshal(launch)
	require.NoError(t, err)
	record := allocationRecord{ID: uuid.NewString(), SessionID: launch.WorkID, SandboxID: "sandbox", Version: 1, PreparationCommand: "prepare", RequestJSON: encoded, SpecificationJSON: []byte(`{}`), CredentialPlanJSON: []byte(`{}`), ServiceAccessJSON: []byte(`[]`), AuthorizationIDs: []byte(`[]`)}
	_, err = db.Insert(&record)
	require.NoError(t, err)
	return record
}

// The start's preparation process receives the plan of the repositories its
// LaunchSpecification leaves to it, and reports an outcome for each planned
// repository: one it did not settle before it ended did not finish.
func TestPreparationReportsAnOutcomeForEveryPlannedRepository(t *testing.T) {
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	launch := request()
	launch.WorkID = uuid.NewString()
	launch.RepositorySelection, launch.RemainingRepositories = checkoutSelection(), []string{"github:1", "github:2"}
	record := preparedRecord(t, db, launch)
	// The process settled github:1 and ended before its report.
	client, created := preparationServer(t, `{"checkout":{"repositoryId":"github:1","targetPath":"/workspace/repos/api","checkedOut":true,"reason":""}}`, 1)
	// Controlplane takes no part in checkout beyond sending the plan: this
	// Provisioning has no storage root, so it cannot write the workspace.
	n := &NativeConnections{provisioning: &Provisioning{records: records{db: db}, now: time.Now}}

	report, err := n.prepareEnvironment(ctx, &record, client, launch)

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(*created, "talgent_checkout_plan='"), "the process receives the plan: %s", *created)
	for _, secret := range []string{"Authorization", "x-access-token", "credential.helper=!", "GIT_ASKPASS"} {
		require.NotContains(t, *created, secret, "the plan carries no credential; the Credential Proxy adds it")
	}
	require.Equal(t, []domain.RepositoryCheckout{
		{RepositoryID: "github:1", CheckedOut: true},
		{RepositoryID: "github:2", Reason: domain.CheckoutIncomplete},
	}, report.Checkouts)
	require.False(t, record.EnvironmentPrepared, "the execution records its preparation ended, after the outcomes")
	stored, found, err := latestAllocation(ctx, db, launch.WorkID)
	require.NoError(t, err)
	require.True(t, found)
	require.JSONEq(t, `[{"repositoryId":"github:1","cloneUrl":"https://github.com/acme/api.git","baseBranch":"main","workBranch":"talgent/TAL-396","targetPath":"/workspace/repos/api"},{"repositoryId":"github:2","cloneUrl":"https://github.com/acme/private.git","baseBranch":"main","workBranch":"talgent/TAL-396","targetPath":"/workspace/repos/private"}]`, string(stored.CheckoutPlan))
}

// A preparation the bound ended before its report times out every planned
// repository it did not settle; an unsafe reason is never shown.
func TestPreparationEndedAtItsBoundTimesOutUnsettledCheckouts(t *testing.T) {
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	launch := request()
	launch.WorkID = uuid.NewString()
	launch.RepositorySelection, launch.RemainingRepositories = checkoutSelection(), []string{"github:1", "github:2"}
	record := preparedRecord(t, db, launch)
	client, _ := preparationServer(t, `{"checkout":{"repositoryId":"github:1","targetPath":"/workspace/repos/api","checkedOut":false,"reason":"fatal: could not read Username"}}`, timeoutExitCode)
	n := &NativeConnections{provisioning: &Provisioning{records: records{db: db}, now: time.Now}}

	report, err := n.prepareEnvironment(ctx, &record, client, launch)

	require.NoError(t, err)
	require.Equal(t, []domain.RepositoryCheckout{
		{RepositoryID: "github:1", Reason: domain.CheckoutUnavailable},
		{RepositoryID: "github:2", Reason: domain.CheckoutTimedOut},
	}, report.Checkouts)
}

// A first start that ended before its checkout ended leaves the repositories
// without an outcome to the next start: only those reach its plan, at the
// targets the Work's selection planned.
func TestNextStartChecksOutOnlyWhatTheFirstStartLeft(t *testing.T) {
	ctx := t.Context()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Fixture Agent", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, definitions.Save(ctx, definition))
	definition, err = definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: "workspace", HomeBinding: definition.HomeBinding, Capabilities: domain.CapabilitySnapshot{Connectors: request().Capabilities.Connectors}}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: fixtureCloudID, Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	work.RepositorySelection = checkoutSelection()
	work.DefinitionVersion = definition.Version
	require.NoError(t, works.Save(ctx, work))
	work, err = works.Get(ctx, work.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"github:1", "github:2"}, work.LaunchSpecification().RemainingRepositories, "the first start checks out every selected repository")
	require.True(t, work.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: work.ID, Checkouts: []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}}}))
	require.NoError(t, works.Save(ctx, work))

	stored, err := works.Get(ctx, work.ID)
	require.NoError(t, err)

	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "github:1", CheckedOut: true}}, stored.RepositoryCheckouts)
	require.Equal(t, []repositoryCheckout{{RepositoryID: "github:2", CloneURL: "https://github.com/acme/private.git", BaseBranch: "main", WorkBranch: "talgent/TAL-396", TargetPath: "/workspace/repos/private"}}, checkoutPlan(stored.LaunchSpecification()))
}
