//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// Pause a real admission lookup's negative response until the competing retry
// has committed and resolved the same request. No sleeps or fake SQL state.
type admissionReadAfterWinner struct {
	command.AgentWorkRepository
	lookups        int
	completeWinner func()
}

func (r *admissionReadAfterWinner) FindStartRequest(ctx context.Context, requestID string) (agentquery.StartRequest, bool, error) {
	result, found, err := r.AgentWorkRepository.FindStartRequest(ctx, requestID)
	r.lookups++
	if err == nil && !found && r.lookups == 2 {
		r.completeWinner()
	}
	return result, found, err
}

func TestConcurrentContinuationRecognizesAlreadyResolvedAdmission(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	starts := 0
	started := startedWorks{AgentWorkRepository: f.sessions, onStart: func(domain.LaunchSpecification) { starts++ }}
	access := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db))
	winner := command.NewResumeWorkHandler(time.Now, started, f.agents, access, computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	admissions := &admissionReadAfterWinner{AgentWorkRepository: started, completeWinner: func() { require.NoError(t, winner.Handle(ctx, request)) }}
	loser := command.NewResumeWorkHandler(time.Now, admissions, f.agents, access, computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	require.NoError(t, loser.Handle(ctx, request), "a stale negative admission read cannot turn a completed identical request into a conflict")
	require.Equal(t, 1, starts, "only the winning retry requests execution")
	admitted, found := admittedStart(t, f.sessions, request.RequestID)
	require.True(t, found)
	require.Equal(t, id, admitted.WorkID)
}
