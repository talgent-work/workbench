package execution

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// A run's Personal Variables reach its Harness only (Personal Variables at
// launch only). execd's process creation (/pty, which the launch needs for a
// reattachable process handle) takes a working directory and a command, no
// environment, and a sandbox's own environment reaches every process,
// preparation included. So the launch writes them, right before it starts the
// Harness and after preparation ended, to a file of its own outside every Work
// Directory, which the fixed, value-free launch command loads into the
// Harness's environment and removes. No value enters the sandbox's
// specification, a command's text or a stored record.
//
// A launch whose process creation fails removes the file again, as far as
// execd still answers; a retried launch uploads it anew in its place, and a
// file left behind ends with the sandbox. The file belongs to execd's user,
// which runs the Harness too.

// sandboxVariableFile is where a Cloud run's Personal Variables wait for its
// Harness launch: in the sandbox's own /tmp, named from the execution's
// identity alone.
func sandboxVariableFile(owner SandboxOwner) string {
	return "/tmp/talgent-variables-" + storageKey(owner.WorkID+":"+owner.ID)
}

// personalVariableLoader is the launch command text that exports the
// variables waiting in file, if any, over the environment. It opens the file
// and removes it before it exports anything, so no exported value, such as a
// person's PATH, can keep the file in place. Entries are NAME=value, each
// ended by NUL, read by a NUL-safe loop, so a value may span lines; a reserved
// name is never exported. It runs before the launch's own derivations from the
// environment, such as the model flag read from ANTHROPIC_MODEL, which
// therefore follow the person's values. The command runs under bash, as execd
// runs every command where the sandbox has it.
func personalVariableLoader(file string) string {
	if file == "" {
		return ""
	}
	return "talgent_variables=" + shellWord(file) + "; " +
		"if test -f \"$talgent_variables\" && exec 3<\"$talgent_variables\"; then rm -f \"$talgent_variables\"; " +
		"while IFS= read -r -d '' talgent_variable <&3; do case \"$talgent_variable\" in " +
		reservedVariablePattern() + ") ;; [A-Za-z_]*=*) export \"$talgent_variable\" ;; esac; done; exec 3<&-; " +
		"fi; unset talgent_variable talgent_variables; "
}

// reservedVariablePattern is a shell case pattern matching a name with the
// reserved prefix in any letter case, as domain.ReservedVariableName does.
func reservedVariablePattern() string {
	var pattern strings.Builder
	for _, r := range domain.ReservedVariablePrefix {
		lower, upper := unicode.ToLower(r), unicode.ToUpper(r)
		if lower == upper {
			pattern.WriteRune(r)
			continue
		}
		pattern.WriteString("[" + string(upper) + string(lower) + "]")
	}
	return pattern.String() + "*"
}

// launchVariables are the variables the launch command itself exports before
// the Personal Variables load, so a Personal Variable replaces them.
var launchVariables = []string{repositoryInstructionsVariable}

// launchUnsetVariables are variables the launch command reads and unsets
// after the Personal Variables load, so no Personal Variable of their name
// replaces a platform variable in the Harness's environment.
var launchUnsetVariables = []string{effortVariable}

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
// Personal Variables take the place of: those the sandbox's specification
// sets and those the launch command exports. Variables the sandbox image
// defines for itself, such as PATH or HOME, are the image's own, not the
// platform's, and a name the launch command unsets before the Harness starts
// replaces nothing there.
func replacedVariables(variables []domain.PersonalVariable, platform map[string]string) []string {
	var replaced []string
	for _, variable := range variables {
		_, set := platform[variable.Name]
		if domain.ReservedVariableName(variable.Name) || slices.Contains(launchUnsetVariables, variable.Name) {
			continue
		}
		if set || slices.Contains(launchVariables, variable.Name) {
			replaced = append(replaced, variable.Name)
		}
	}
	slices.Sort(replaced)
	return slices.Compact(replaced)
}

// variableDelivery is an authority that hands a run's Personal Variables to
// its Harness launch. One without it launches the Harness without them.
type variableDelivery interface {
	// deliverVariables places the variables where the launch command loads
	// them and names the platform variables they replace.
	deliverVariables(ctx context.Context, record executionRecord, client *execd.Client, variables []domain.PersonalVariable) ([]string, error)
	// withdrawVariables removes delivered variables no launch loaded.
	withdrawVariables(ctx context.Context, record executionRecord, client *execd.Client) error
}

var _ variableDelivery = cloudEnvironment{}

// deliverVariables uploads a Cloud run's Personal Variables to its file,
// readable by the sandbox user alone, and names the variables of the
// sandbox's specification and of the launch command they replace.
func (cloudEnvironment) deliverVariables(ctx context.Context, record executionRecord, client *execd.Client, variables []domain.PersonalVariable) ([]string, error) {
	var spec sdk.CreateSandboxRequest
	if err := json.Unmarshal(record.SpecificationJSON, &spec); err != nil {
		return nil, oops.With("operation", "decode_native_spec").Wrap(err)
	}
	if err := client.Upload(ctx, cloudVariableFile(record), 0o600, encodePersonalVariables(variables)); err != nil {
		return nil, oops.With("operation", "deliver_personal_variables").Wrap(err)
	}
	return replacedVariables(variables, spec.Env), nil
}

// withdrawVariables removes a Cloud run's file of Personal Variables.
func (cloudEnvironment) withdrawVariables(ctx context.Context, record executionRecord, client *execd.Client) error {
	if err := client.Remove(ctx, cloudVariableFile(record)); err != nil {
		return oops.With("operation", "withdraw_personal_variables").Wrap(err)
	}
	return nil
}

func cloudVariableFile(record executionRecord) string {
	return sandboxVariableFile(SandboxOwner{WorkID: record.SessionID, ID: record.ID})
}
