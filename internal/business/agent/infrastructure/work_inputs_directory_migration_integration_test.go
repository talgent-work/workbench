//go:build integration

package infrastructure_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

const workInputsDirectoryMigration = "../../../../deploy/middleware/migrations/20261120-work-inputs-directory.sql"

// storedWork is a Work row as an earlier release stored it, before Works
// recorded their inputs Directory key.
type storedWork struct {
	id     string
	state  int
	inputs *domain.Directory
}

func insertStoredWork(t *testing.T, db *sql.DB, work storedWork) {
	t.Helper()
	type fact struct {
		Condition string `json:"condition"`
		Value     string `json:"value"`
	}
	type directory struct {
		Owner    []fact `json:"owner"`
		Name     string `json:"name"`
		Backend  string `json:"backend"`
		Lifetime string `json:"lifetime"`
	}
	layout := struct {
		Mounts            []any      `json:"mounts"`
		Inputs            *directory `json:"inputs,omitempty"`
		PublicationTarget directory  `json:"publication_target"`
	}{Mounts: []any{}, PublicationTarget: directory{Owner: []fact{{"work", work.id}}, Name: "artifacts", Backend: "juicefs", Lifetime: "permanent"}}
	if work.inputs != nil {
		inputs := directory{Owner: []fact{}, Name: work.inputs.Name, Backend: "juicefs", Lifetime: "permanent"}
		for _, owner := range work.inputs.Owner {
			inputs.Owner = append(inputs.Owner, fact{string(owner.Condition), owner.Value})
		}
		layout.Inputs = &inputs
	}
	encoded, err := json.Marshal(layout)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO agent_session (id, agent_id, started_by, work_caller_kind, work_caller_id, state, capabilities, context_ref, agent_harness, located_layout, trace_attributes, harness_process, failure_reason, version)
 VALUES (?, ?, 'owner', 'person', 'owner', ?, '{}', '', 'claude-code', ?, '{}', '{}', '', 1)`, work.id, uuid.NewString(), work.state, string(encoded))
	require.NoError(t, err)
}

func createTable(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name, statement string
	require.NoError(t, db.QueryRowContext(t.Context(), "SHOW CREATE TABLE agent_session").Scan(&name, &statement))
	return statement
}

// The migration brings a Work table of the earlier release to the current
// schema, and fills each unclosed Work's inputs Directory key as the
// controlplane computes it, whatever characters its owner facts hold; the
// Works a file change concerns are then found by it.
func TestWorkInputsDirectoryMigrationKeysEachUnclosedWorksInputs(t *testing.T) {
	ctx := context.Background()
	config, err := mysql.ParseDSN(mysqltest.Database(t))
	require.NoError(t, err)
	config.MultiStatements = true
	db, err := sql.Open("mysql", config.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	current := createTable(t, db)
	_, err = db.ExecContext(ctx, "ALTER TABLE agent_session DROP KEY idx_inputs_directory_deleted_at_id, DROP COLUMN inputs_directory")
	require.NoError(t, err)

	own := uuid.NewString()
	homed := domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionFileHome, Value: `case:<a & "b"> \ 長` + "  "}}, Name: "inputs"}
	works := map[string]storedWork{
		"own":     {id: own, state: 4, inputs: &domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: own}}, Name: "inputs"}},
		"homed":   {id: uuid.NewString(), state: 2, inputs: &homed},
		"closing": {id: uuid.NewString(), state: 6, inputs: &homed},
		"closed":  {id: uuid.NewString(), state: 7, inputs: &homed},
		"none":    {id: uuid.NewString(), state: 2},
	}
	for _, work := range works {
		insertStoredWork(t, db, work)
	}

	script, err := os.ReadFile(workInputsDirectoryMigration)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(script))
	require.NoError(t, err)
	require.Equal(t, current, createTable(t, db), "the migrated table is the current schema's")

	keys := map[string]string{}
	for name, work := range works {
		var key string
		require.NoError(t, db.QueryRowContext(ctx, "SELECT inputs_directory FROM agent_session WHERE id = ?", work.id).Scan(&key))
		keys[name] = key
	}
	require.Equal(t, map[string]string{
		"own": infrastructure.DirectoryKey(*works["own"].inputs), "homed": infrastructure.DirectoryKey(homed),
		"closing": infrastructure.DirectoryKey(homed), "closed": "", "none": "",
	}, keys)

	engine, err := xorm.NewEngine("mysql", config.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = engine.Close() })
	jdb, err := jimuxorm.New(engine)
	require.NoError(t, err)
	found, err := infrastructure.NewWorkQueryRepository(jdb).ListUnclosedWorksReadingInputs(ctx, homed, "", 10)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{works["homed"].id, works["closing"].id}, found)
}
