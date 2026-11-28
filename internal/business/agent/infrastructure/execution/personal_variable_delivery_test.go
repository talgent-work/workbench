package execution

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// harnessRecord is what the recording Harness saw: its arguments and
// environment.
type harnessRecord struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

// runCloudLaunch runs a Cloud launch command under bash, as execd does, with
// the run's variable file holding content when it is not nil, and returns
// what the Harness saw.
func runCloudLaunch(t *testing.T, thinkingLevel string, content []byte, env ...string) (harnessRecord, nativeLaunch, string) {
	t.Helper()
	root := t.TempDir()
	output := filepath.Join(root, "harness.json")
	binary := filepath.Join(root, "claude")
	require.NoError(t, os.WriteFile(binary, []byte("#!/usr/bin/env python3\nimport os,sys,json\nopen(os.environ['RECORD_FILE'],'w').write(json.dumps({'args':sys.argv[1:],'env':dict(os.environ)}))\n"), 0o700))
	input := request()
	input.WorkID = uuid.NewString()
	input.ContextMode = domain.ContextFresh
	launch, err := NativeLaunchConfig{Binary: binary}.prepare(sandboxLayout, input, owner(input), thinkingLevel)
	require.NoError(t, err)
	file := sandboxVariableFile(owner(input))
	t.Cleanup(func() {
		_ = os.Remove(file)
		_ = os.Remove(sandboxDiagnostics + diagnosticName(owner(input)))
	})
	if content != nil {
		require.NoError(t, os.WriteFile(file, content, 0o600))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", launch.Command)
	cmd.Env = append(os.Environ(), append([]string{"RECORD_FILE=" + output}, env...)...)
	require.NoError(t, cmd.Run())
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var record harnessRecord
	require.NoError(t, json.Unmarshal(data, &record))
	return record, launch, file
}

func argument(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// A person's values reach the Harness as they are, in place of the platform's
// own and of what the launch command exports, and the launch's derivations
// follow them; the platform's TALGENT_ variables are never replaced, and the
// file is gone once loaded.
func TestCloudLaunchLayersPersonalVariablesOverThePlatformsForTheHarness(t *testing.T) {
	injected := filepath.Join(t.TempDir(), "injected")
	pem := "-----BEGIN KEY-----\nline ' \"two\" $(touch " + injected + ") `false` $HOME\n-----END KEY-----\n"
	variables := []domain.PersonalVariable{
		{Name: "ANTHROPIC_MODEL", Value: "person-model"},
		{Name: "ANTHROPIC_BASE_URL", Value: "https://models.person.example"},
		{Name: repositoryInstructionsVariable, Value: "0"},
		{Name: "PEM", Value: pem},
		{Name: "EMPTY", Value: ""},
		{Name: "TALGENT_WORK_ID", Value: "replaced"},
	}
	content := encodePersonalVariables(variables)
	require.NotContains(t, string(content), "TALGENT_WORK_ID", "a reserved name is never delivered")
	// The loader refuses reserved names too, whatever the file holds.
	content = append(content, []byte("TALGENT_WORK_ID=replaced\x00talgent_root=replaced\x00")...)

	record, launch, file := runCloudLaunch(t, "", content,
		"ANTHROPIC_MODEL=platform-model", "ANTHROPIC_BASE_URL=https://platform.example", "TALGENT_WORK_ID=work", "PLATFORM_ONLY=kept")

	require.Equal(t, "person-model", record.Env["ANTHROPIC_MODEL"])
	require.Equal(t, "person-model", argument(record.Args, "--model"), "the model flag follows the person's ANTHROPIC_MODEL")
	require.Equal(t, "https://models.person.example", record.Env["ANTHROPIC_BASE_URL"])
	require.Equal(t, "0", record.Env[repositoryInstructionsVariable], "a person's value wins over the launch command's own")
	require.Equal(t, pem, record.Env["PEM"], "a multi-line value arrives byte for byte")
	require.Contains(t, record.Env, "EMPTY")
	require.Equal(t, "work", record.Env["TALGENT_WORK_ID"])
	require.NotContains(t, record.Env, "talgent_root")
	require.Equal(t, "kept", record.Env["PLATFORM_ONLY"])
	require.NoFileExists(t, injected, "a value is never evaluated by the shell")
	require.NoFileExists(t, file, "the launch removes the file once loaded")
	for _, variable := range variables {
		if variable.Value != "" && variable.Value != "0" {
			require.NotContains(t, launch.Command, variable.Value, "the command text carries no value")
		}
	}
}

// The thinking level stays the Harness argument it is, whatever the person's
// CLAUDE_CODE_EFFORT_LEVEL says; without one, the person's value is the
// launch's default effort as the platform's would be.
func TestCloudLaunchKeepsTheThinkingLevelAnArgument(t *testing.T) {
	content := encodePersonalVariables([]domain.PersonalVariable{{Name: "CLAUDE_CODE_EFFORT_LEVEL", Value: "low"}})
	record, _, _ := runCloudLaunch(t, "high", content, "CLAUDE_CODE_EFFORT_LEVEL=medium")
	require.Equal(t, "high", argument(record.Args, "--effort"))
	require.NotContains(t, record.Env, "CLAUDE_CODE_EFFORT_LEVEL")

	record, _, _ = runCloudLaunch(t, "", content, "CLAUDE_CODE_EFFORT_LEVEL=medium")
	require.Equal(t, "low", argument(record.Args, "--effort"))
}

// A run without Personal Variables launches as before.
func TestCloudLaunchWithoutPersonalVariablesKeepsThePlatformsEnvironment(t *testing.T) {
	record, _, _ := runCloudLaunch(t, "", nil, "ANTHROPIC_MODEL=platform-model")
	require.Equal(t, "platform-model", record.Env["ANTHROPIC_MODEL"])
	require.Equal(t, "platform-model", argument(record.Args, "--model"))
	require.Equal(t, "1", record.Env[repositoryInstructionsVariable])
}

func TestReplacedVariablesNameThePlatformsOwnInNameOrder(t *testing.T) {
	variables := []domain.PersonalVariable{{Name: "PERSONAL_ONLY"}, {Name: "ANTHROPIC_MODEL"}, {Name: repositoryInstructionsVariable}, {Name: "ANTHROPIC_BASE_URL"}, {Name: "TALGENT_WORK_ID"}}
	platform := map[string]string{"ANTHROPIC_MODEL": "m", "ANTHROPIC_BASE_URL": "u", "TALGENT_WORK_ID": "w", "HOME": "/home/talgent"}
	require.Equal(t, []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", repositoryInstructionsVariable}, replacedVariables(variables, platform))
	require.Empty(t, replacedVariables(nil, platform))
}
