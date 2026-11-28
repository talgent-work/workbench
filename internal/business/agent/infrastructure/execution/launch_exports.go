package execution

import (
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// launchVariable is a variable the Harness launch command exports itself,
// with its value as shell text.
type launchVariable struct {
	name, value string
}

// launchExports is the launch command text that exports own, except each a
// run's Personal Variables set (personal names them): the Personal Variable
// takes its place (Variable precedence). On a Managed Computer the values are
// already in the Harness's environment when the command starts, so an export
// of the launch's own would replace them; on a Cloud the loader that runs
// after these exports would restore them anyway.
func launchExports(own []launchVariable, personal []string) string {
	var out strings.Builder
	for _, variable := range own {
		if !slices.Contains(personal, variable.name) {
			out.WriteString("export " + variable.name + "=" + variable.value + "; ")
		}
	}
	return out.String()
}

// personalNames names a run's Personal Variables, a reserved name excepted.
func personalNames(variables []domain.PersonalVariable) []string {
	var names []string
	for _, variable := range variables {
		if !domain.ReservedVariableName(variable.Name) {
			names = append(names, variable.Name)
		}
	}
	return names
}
