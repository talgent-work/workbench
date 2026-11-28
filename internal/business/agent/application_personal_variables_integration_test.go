//go:build integration

package agent_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing/sealingtest"
)

// refusal is the message an API refusal carries.
func refusal(t *testing.T, err error) string {
	t.Helper()
	var refused *connect.Error
	require.ErrorAs(t, err, &refused)
	return refused.Message()
}

func (c ownerClients) setVariable(name, value string) error {
	_, err := c.variables.SetPersonalVariable(c.ctx, &agentpublicv1.SetPersonalVariableRequest{Name: name, Value: value})
	return err
}

func (c ownerClients) variableNames(t *testing.T) []string {
	t.Helper()
	response, err := c.variables.ListPersonalVariables(c.ctx, &agentpublicv1.ListPersonalVariablesRequest{})
	require.NoError(t, err)
	var names []string
	for _, variable := range response.GetVariables() {
		names = append(names, variable.GetName())
	}
	return names
}

func (c ownerClients) reveal(name string) (string, error) {
	response, err := c.variables.RevealPersonalVariable(c.ctx, &agentpublicv1.RevealPersonalVariableRequest{Name: name})
	return response.GetValue(), err
}

// personalVariableSets reaches the module's PersonalVariableSets as stored.
func personalVariableSets(t *testing.T, m *lifecycleModule) *infrastructure.PersonalVariableSetRepository {
	t.Helper()
	jdb, err := jimuxorm.New(m.db)
	require.NoError(t, err)
	return infrastructure.NewPersonalVariableSetRepository(jdb, sealingtest.Sealer(), time.Now)
}

// A person sets, lists by name, reveals and deletes their own Personal
// Variables, multi-line values kept as they are; another person reads and
// changes none of them, and no other principal reaches the service.
func TestAPersonKeepsTheirOwnPersonalVariables(t *testing.T) {
	m := newLifecycleModule(t).start()
	person, other := m.as("user", uuid.NewString()), m.as("user", uuid.NewString())
	pem := "-----BEGIN KEY-----\nAAAA\n-----END KEY-----\n"

	require.NoError(t, person.setVariable("API_TOKEN", "first-secret"))
	require.NoError(t, person.setVariable("PEM", pem))
	require.NoError(t, person.setVariable("API_TOKEN", "second-secret"))
	require.Equal(t, []string{"API_TOKEN", "PEM"}, person.variableNames(t))
	value, err := person.reveal("API_TOKEN")
	require.NoError(t, err)
	require.Equal(t, "second-secret", value, "setting a name again replaces its value")
	value, err = person.reveal("PEM")
	require.NoError(t, err)
	require.Equal(t, pem, value)

	var stored struct {
		Variables []byte `xorm:"variables"`
	}
	_, err = m.db.SQL("SELECT variables FROM agent_personal_variable_set").Get(&stored)
	require.NoError(t, err)
	require.NotEmpty(t, stored.Variables)
	require.NotContains(t, string(stored.Variables), "second-secret", "values are sealed at rest")
	require.NotContains(t, string(stored.Variables), "API_TOKEN")

	require.Empty(t, other.variableNames(t), "another person lists none of them")
	_, err = other.reveal("API_TOKEN")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another person reveals none of them")
	require.NoError(t, other.setVariable("API_TOKEN", "others-own"))
	_, err = other.variables.DeletePersonalVariable(other.ctx, &agentpublicv1.DeletePersonalVariableRequest{Name: "PEM"})
	require.NoError(t, err)
	value, err = person.reveal("API_TOKEN")
	require.NoError(t, err)
	require.Equal(t, "second-secret", value, "another person's variable of the same name is their own")
	require.Equal(t, []string{"API_TOKEN", "PEM"}, person.variableNames(t))

	for _, caller := range []ownerClients{m.as("application", supportDesk)} {
		_, err = caller.variables.ListPersonalVariables(caller.ctx, &agentpublicv1.ListPersonalVariablesRequest{})
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "only a person reaches Personal Variables")
	}

	_, err = person.variables.DeletePersonalVariable(person.ctx, &agentpublicv1.DeletePersonalVariableRequest{Name: "API_TOKEN"})
	require.NoError(t, err)
	_, err = person.variables.DeletePersonalVariable(person.ctx, &agentpublicv1.DeletePersonalVariableRequest{Name: "API_TOKEN"})
	require.NoError(t, err, "deleting a name the person does not keep succeeds")
	_, err = person.reveal("API_TOKEN")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	require.Equal(t, []string{"PEM"}, person.variableNames(t))
}

// Setting refuses, naming the broken rule as a person reads it and never the
// value, a name that is not an environment variable name, a reserved name, a
// value with NUL, a value over 32 KiB, names and values together over 256 KiB,
// and a 101st variable; a refusal changes nothing.
func TestSettingRefusesWhatBreaksAPersonalVariableRule(t *testing.T) {
	m := newLifecycleModule(t).start()
	subject := uuid.NewString()
	person := m.as("user", subject)
	refused := func(name, value, rule string) {
		t.Helper()
		err := person.setVariable(name, value)
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), name)
		require.Contains(t, refusal(t, err), rule)
		require.NotContains(t, refusal(t, err), "agent:", "the person reads the rule, not an internal error")
		if len(value) > 0 {
			require.NotContains(t, err.Error(), value, "a refusal never quotes the value")
		}
	}
	refused("1TOKEN", "refused-value", "letter or underscore")
	refused("API-TOKEN", "refused-value", "letter or underscore")
	refused("TALGENT_WORK_ID", "refused-value", "reserved")
	refused("talgent_root", "refused-value", "in any letter case")
	refused("TOKEN", "secret\x00value", "NUL")
	refused("LARGE", strings.Repeat("x", 32<<10+1), "32 KiB")

	for i := range 7 {
		require.NoError(t, person.setVariable(fmt.Sprintf("BLOCK_%d", i), strings.Repeat("x", 32<<10)))
	}
	refused("BLOCK_7", strings.Repeat("y", 32<<10), "256 KiB")
	require.Len(t, person.variableNames(t), 7, "a refused variable is not kept")

	// A person who keeps 100 variables keeps no 101st.
	other := uuid.NewString()
	full, err := domain.NewPersonalVariableSet(other)
	require.NoError(t, err)
	for i := range 100 {
		_, err = full.SetVariable(other, domain.PersonalVariable{Name: fmt.Sprintf("VARIABLE_%03d", i), Value: "v"})
		require.NoError(t, err)
	}
	require.NoError(t, personalVariableSets(t, m).Save(m.ctx, full))
	crowded := m.as("user", other)
	err = crowded.setVariable("VARIABLE_100", "v")
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	require.Contains(t, refusal(t, err), "at most 100")
	require.NoError(t, crowded.setVariable("VARIABLE_000", "replaced"), "replacing a kept value stays within the count")
	require.Len(t, crowded.variableNames(t), 100)
}
