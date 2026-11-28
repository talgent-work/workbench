package execution

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// runManagedLaunch runs a Managed launch command for a run whose Personal
// Variables set personal under bash, as Talgent Computer does, in env, where
// Talgent Computer already layered the values, and returns what the Harness
// saw.
func runManagedLaunch(t *testing.T, personal []string, env ...string) harnessRecord {
	t.Helper()
	root := t.TempDir()
	output := filepath.Join(root, "harness.json")
	binary := filepath.Join(root, "claude")
	require.NoError(t, os.WriteFile(binary, []byte("#!/usr/bin/env python3\nimport os,sys,json\nopen(os.environ['RECORD_FILE'],'w').write(json.dumps({'args':sys.argv[1:],'env':dict(os.environ)}))\n"), 0o700))
	input := managedRequest()
	input.WorkID = uuid.NewString()
	input.ContextMode = domain.ContextFresh
	launch, err := NativeLaunchConfig{Binary: binary}.prepareRun(machineLayout, input, owner(input), "", personal)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", launch.Command)
	cmd.Env = append(os.Environ(), append([]string{"RECORD_FILE=" + output, "TALGENT_WORK_ROOT=" + root}, env...)...)
	require.NoError(t, cmd.Run())
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var record harnessRecord
	require.NoError(t, json.Unmarshal(data, &record))
	return record
}

// On a Managed Computer the Personal Variables are in the Harness's
// environment before its launch command runs, so the command leaves the
// variables it would export itself to them, and its derivations follow
// them; a run without them keeps the launch's own.
func TestManagedLaunchLeavesItsOwnVariablesToThePersonalVariables(t *testing.T) {
	record := runManagedLaunch(t, []string{pluginCacheVariable, repositoryInstructionsVariable, "ANTHROPIC_MODEL"},
		pluginCacheVariable+"=/person-plugins", repositoryInstructionsVariable+"=0", "ANTHROPIC_MODEL=person-model")
	require.Equal(t, "/person-plugins", record.Env[pluginCacheVariable])
	require.Equal(t, "0", record.Env[repositoryInstructionsVariable])
	require.Equal(t, "person-model", argument(record.Args, "--model"))

	record = runManagedLaunch(t, nil, pluginCacheVariable+"=/machine-plugins", repositoryInstructionsVariable+"=0")
	require.True(t, strings.HasSuffix(record.Env[pluginCacheVariable], "/.talgent/claude-plugins"), "the launch's own without Personal Variables")
	require.Equal(t, "1", record.Env[repositoryInstructionsVariable])
}

// A Managed run's Personal Variables replace the variables of its
// specification's environment, those Talgent Computer sets as the
// specification asks and those its launch command exports, never a reserved
// one or the machine's own; a set the relay cannot carry fails the start
// definitely, naming no value.
func TestManagedPersonalVariablesNameWhatTheyReplaceAndFitTheRelay(t *testing.T) {
	configured := managedRequest()
	configured.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	spec, err := managedSpecificationFor(configured, fixtureModels(), "execd-token")
	require.NoError(t, err)
	spec.Telemetry.Token = "telemetry-token"
	document, err := json.Marshal(spec)
	require.NoError(t, err)
	record := executionRecord{SpecificationJSON: document, LaunchCommand: "exec claude"}

	variables := []domain.PersonalVariable{
		{Name: "ANTHROPIC_BASE_URL", Value: "u"}, {Name: "ANTHROPIC_AUTH_TOKEN", Value: "k"}, {Name: "CLAUDE_CODE_SUBAGENT_MODEL", Value: "m"},
		{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "e"}, {Name: "PATH", Value: "/bin"}, {Name: "MINE", Value: "v"}, {Name: "TALGENT_WORK_ID", Value: "w"},
	}
	handover, err := ManagedEnvironment{}.deliverVariables(context.Background(), record, nil, variables)
	require.NoError(t, err)
	require.Equal(t, []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_SUBAGENT_MODEL", "OTEL_EXPORTER_OTLP_ENDPOINT"}, handover.replaced)
	require.Equal(t, map[string]string{"ANTHROPIC_BASE_URL": "u", "ANTHROPIC_AUTH_TOKEN": "k", "CLAUDE_CODE_SUBAGENT_MODEL": "m", "OTEL_EXPORTER_OTLP_ENDPOINT": "e", "PATH": "/bin", "MINE": "v"}, handover.carried)

	// Control characters take six bytes each in the request's JSON.
	oversized := []domain.PersonalVariable{{Name: "BIG", Value: strings.Repeat("\x01", 200<<10)}}
	_, err = ManagedEnvironment{}.deliverVariables(context.Background(), record, nil, oversized)
	cause, definite := domain.DefiniteStartCause(err)
	require.True(t, definite)
	require.Equal(t, "the Personal Variables are too large to reach a Managed Computer", cause)
}
