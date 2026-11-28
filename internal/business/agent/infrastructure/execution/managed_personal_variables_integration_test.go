//go:build integration

package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// managedVariables are a run's Personal Variables that replace platform
// variables of each kind on a Managed Computer, with one of the person's own,
// a multi-line one, one the platform does not set on the machine's own
// Claude login and a reserved one.
var managedVariables = []domain.PersonalVariable{
	{Name: "API_TOKEN", Value: "secret-token-value"},
	{Name: "DISABLE_AUTOUPDATER", Value: "person-autoupdate"},
	{Name: "HOME", Value: "/person-home"},
	{Name: pluginCacheVariable, Value: "/person-plugins"},
	{Name: repositoryInstructionsVariable, Value: "0"},
	{Name: "ANTHROPIC_BASE_URL", Value: "https://person-model.example"},
	{Name: "PEM", Value: "-----BEGIN KEY-----\nAAAA\n-----END KEY-----\n"},
	{Name: "TALGENT_WORK_ID", Value: "replaced-work"},
}

// A Managed run's Personal Variables reach its Harness launch only: its
// process creation carries them, reserved names left out, while the
// preparation's creation and the Work's environment, which every process
// gets, carry none, and neither the stored specification nor any command's
// text holds a value. The launch command leaves its own variables that they
// set to them, and the run records the platform variables they replaced.
func TestTheManagedHarnessLaunchAloneCarriesTheRunsPersonalVariables(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh

	record := f.launch(t, f.beginWith(t, input, managedVariables).ID)

	relay := f.relay
	require.Len(t, relay.created, 2)
	require.Nil(t, relay.created[0].PersonalVariables, "the preparation carries none")
	want := map[string]string{}
	for _, variable := range managedVariables[:len(managedVariables)-1] {
		want[variable.Name] = variable.Value
	}
	require.Equal(t, want, relay.created[1].PersonalVariables, "the Harness launch carries them, the reserved one left out")
	require.Equal(t, record.LaunchCommand, relay.created[1].Command)

	require.NotContains(t, record.LaunchCommand, "export "+pluginCacheVariable+"=", "the launch leaves the variables they set to them")
	require.NotContains(t, record.LaunchCommand, "export "+repositoryInstructionsVariable+"=")
	require.Contains(t, record.PreparationCommand, "export "+pluginCacheVariable+"=", "the preparation keeps its own")
	for name, surface := range map[string]string{
		"Work environment": strings.Join(mapValues(relay.environment), "\x00"), "stored specification": string(record.SpecificationJSON),
		"launch command": record.LaunchCommand, "preparation command": record.PreparationCommand, "launch specification": string(record.RequestJSON),
	} {
		for _, value := range []string{"secret-token-value", "person-autoupdate", "/person-home", "/person-plugins", "person-model", "AAAA", "replaced-work"} {
			require.NotContains(t, surface, value, "no value reaches the %s", name)
		}
	}

	launched := current(t, f.steps, record.ID)
	require.Equal(t, []string{repositoryInstructionsVariable, pluginCacheVariable, "DISABLE_AUTOUPDATER", "HOME"}, launched.ReplacedVariables,
		"the platform's own variables they replaced; the machine's own Claude login sets no model endpoint")
	replaced, err := NewExecutionQueryRepository(f.provisioning.db).ReadReplacedVariables(ctx, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, launched.ReplacedVariables, replaced)
}

// A machine whose Talgent Computer does not acknowledge the Personal
// Variables, one that predates them, never starts the Harness without them:
// the reserved process is deleted and the launch is retried, and it starts
// once the machine acknowledges them.
func TestAManagedHarnessNeverStartsWithoutItsPersonalVariables(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	f.relay.set(func(m *machineRelay) { m.ignoresVariables = true })
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	begun := f.beginWith(t, input, managedVariables)

	for range 4 {
		_ = f.steps.reconcile(ctx, begun.ID)
	}
	record, _, err := f.provisioning.get(ctx, begun.ID)
	require.NoError(t, err)
	require.Empty(t, record.ProcessID, "no Harness without its variables")
	require.Contains(t, f.relay.deleted, "harness-handle", "the process reserved without them is deleted")

	f.relay.set(func(m *machineRelay) { m.ignoresVariables = false })
	record = f.launch(t, begun.ID)
	require.Equal(t, "harness-handle", record.ProcessID)
	require.NotNil(t, f.relay.created[len(f.relay.created)-1].PersonalVariables)
}

func mapValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}
