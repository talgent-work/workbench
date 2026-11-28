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

// exports are the variables the layout's Harness launch command exports
// itself before any Personal Variable loads: the layout's own, then the one
// every layout's launch needs.
func (l launchLayout) exports() []launchVariable {
	return append(slices.Clone(l.own), repositoryInstructions)
}

// launchExports is the launch command text that exports own, except each a
// run's Personal Variables set (personal names them): a Personal Variable
// takes the place of any variable of the same name. On a Managed Computer the
// values are already in the Harness's environment when the command starts,
// so an export of the launch's own would replace them; on a Cloud the loader
// that runs after these exports would restore them anyway. A name that
// matches only without case is the same variable on Windows alone, where the
// command runs in Git Bash (OSTYPE msys or cygwin), so its export is left out
// there and kept elsewhere.
func launchExports(own []launchVariable, personal []string) string {
	var out strings.Builder
	for _, variable := range own {
		export := "export " + variable.name + "=" + variable.value + "; "
		switch {
		case slices.Contains(personal, variable.name):
		case slices.ContainsFunc(personal, func(name string) bool { return strings.EqualFold(name, variable.name) }):
			out.WriteString("case \"${OSTYPE-}\" in msys*|cygwin*) ;; *) " + export + ";; esac; ")
		default:
			out.WriteString(export)
		}
	}
	return out.String()
}

// personalNames names a run's Personal Variables, a reserved name excepted.
func personalNames(variables []domain.PersonalVariable) []string {
	var names []string
	for name := range carriedVariables(variables) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// carriedVariables are a run's Personal Variables as they reach its Harness:
// by name, a reserved name left out.
func carriedVariables(variables []domain.PersonalVariable) map[string]string {
	carried := make(map[string]string, len(variables))
	for _, variable := range variables {
		if !domain.ReservedVariableName(variable.Name) {
			carried[variable.Name] = variable.Value
		}
	}
	return carried
}
