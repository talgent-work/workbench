package query_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// replacedVariableReads answers the replaced variables of a Work's current
// run and counts its reads.
type replacedVariableReads struct {
	query.ExecutionQueryRepository
	names []string
	reads int
}

func (r *replacedVariableReads) ReadReplacedVariables(context.Context, string) ([]string, error) {
	r.reads++
	return r.names, nil
}

// A Work's startup information names the platform variables its current
// run's Personal Variables replaced to the person its Agent represents
// alone; another reader of the Work learns nothing of them.
func TestOnlyTheRepresentedPersonReadsTheReplacedVariables(t *testing.T) {
	for actor, want := range map[string][]string{"person": {"ANTHROPIC_MODEL"}, "co-owner": nil} {
		ports := &historyPorts{owner: actor, value: query.WorkView{WorkID: "session", State: "working", RepresentedPerson: "person", StartupRequest: &query.WorkStartupRequest{RequestID: "session"}}}
		executions := &replacedVariableReads{names: []string{"ANTHROPIC_MODEL"}}
		value, err := query.NewGetWorkViewHandler(ports, ports, executions).Handle(context.Background(), query.GetWorkView{ActorID: actor, WorkID: "session"})
		require.NoError(t, err)
		require.Equal(t, want, value.StartupRequest.ReplacedVariables, actor)
		if want == nil {
			require.Zero(t, executions.reads, "another reader's view never reads them")
		}
	}
}
