package execution

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// A run's Personal Variables reach its Harness only (Personal Variables at
// launch only). execd's process creation takes a working directory and a
// command, no environment, and a sandbox's own environment reaches every
// process, preparation included. So the launch writes them, right before it
// starts the Harness and after preparation ended, to a file of its own outside
// every Work Directory, which the fixed, value-free launch command loads into
// the Harness's environment and removes. No value enters the sandbox's
// specification, a command's text or a stored record.

// sandboxVariableFile is where a Cloud run's Personal Variables wait for its
// Harness launch: in the sandbox's own /tmp, named from the execution's
// identity alone.
func sandboxVariableFile(owner SandboxOwner) string {
	return "/tmp/talgent-variables-" + storageKey(owner.WorkID+":"+owner.ID)
}

// personalVariableLoader is the launch command text that exports the
// variables waiting in file, if any, over the environment and removes the
// file. Entries are NAME=value, each ended by NUL, read by a NUL-safe loop,
// so a value may span lines; a reserved name is never exported. It runs
// before the launch's own derivations from the environment, such as the
// model flag read from ANTHROPIC_MODEL, which therefore follow the person's
// values. The command runs under bash, as execd runs every command where the
// sandbox has it.
func personalVariableLoader(file string) string {
	if file == "" {
		return ""
	}
	return "talgent_variables=" + shellWord(file) + "; if test -f \"$talgent_variables\"; then " +
		"while IFS= read -r -d '' talgent_variable; do case \"$talgent_variable\" in " +
		reservedVariablePattern + ") ;; [A-Za-z_]*=*) export \"$talgent_variable\" ;; esac; done < \"$talgent_variables\"; " +
		"rm -f \"$talgent_variables\"; fi; unset talgent_variable talgent_variables; "
}

// reservedVariablePattern matches a name with the reserved prefix in any
// case, as domain.ReservedVariableName does.
const reservedVariablePattern = "[Tt][Aa][Ll][Gg][Ee][Nn][Tt]_*"

// launchVariables are the variables the launch command itself exports before
// the Personal Variables load, so a Personal Variable replaces them.
var launchVariables = []string{repositoryInstructionsVariable}

// encodePersonalVariables renders the variables for the launch's loader,
// leaving out reserved names.
func encodePersonalVariables(variables []domain.PersonalVariable) []byte {
	var out strings.Builder
	for _, variable := range variables {
		if domain.ReservedVariableName(variable.Name) || strings.ContainsRune(variable.Name+variable.Value, 0) {
			continue
		}
		out.WriteString(variable.Name + "=" + variable.Value + "\x00")
	}
	return []byte(out.String())
}

// replacedVariables names, in name order, the platform variables the
// Personal Variables take the place of: those the environment sets and
// those the launch command exports.
func replacedVariables(variables []domain.PersonalVariable, platform map[string]string) []string {
	var replaced []string
	for _, variable := range variables {
		_, set := platform[variable.Name]
		if !domain.ReservedVariableName(variable.Name) && (set || slices.Contains(launchVariables, variable.Name)) {
			replaced = append(replaced, variable.Name)
		}
	}
	slices.Sort(replaced)
	return slices.Compact(replaced)
}

// variableDelivery is an authority that hands a run's Personal Variables to
// its Harness launch. One without it launches the Harness without them.
type variableDelivery interface {
	// deliverVariables hands the variables over for the launch, placing them
	// where the launch command loads them or giving them to the Harness's
	// process creation to carry, and names the platform variables they
	// replace.
	deliverVariables(ctx context.Context, record executionRecord, client *execd.Client, variables []domain.PersonalVariable) (variableHandover, error)
}

// variableHandover is how a launch hands over a run's Personal Variables:
// the platform variables they replace and, where the Harness's process
// creation itself carries them, the variables it carries; nil where they
// were placed for the launch command to load.
type variableHandover struct {
	replaced []string
	carried  map[string]string
}

var _ variableDelivery = cloudEnvironment{}

// deliverVariables uploads a Cloud run's Personal Variables to its file,
// readable by the sandbox user alone, and names the variables of the
// sandbox's specification and of the launch command they replace.
func (cloudEnvironment) deliverVariables(ctx context.Context, record executionRecord, client *execd.Client, variables []domain.PersonalVariable) (variableHandover, error) {
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return variableHandover{}, oops.With("operation", "decode_native_spec").Wrap(err)
	}
	file := sandboxVariableFile(SandboxOwner{WorkID: record.SessionID, ID: record.ID})
	if err := client.Upload(ctx, file, 0o600, encodePersonalVariables(variables)); err != nil {
		return variableHandover{}, oops.With("operation", "deliver_personal_variables").Wrap(err)
	}
	return variableHandover{replaced: replacedVariables(variables, spec.Env)}, nil
}
