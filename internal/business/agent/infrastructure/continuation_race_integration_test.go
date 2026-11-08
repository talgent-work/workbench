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
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// Pause a real admission just before its commit until the competing repeat
// of the same request has committed. No sleeps or fake SQL state.
type admissionCommitAfterWinner struct {
	domain.AgentWorkRepository
	saves          int
	completeWinner func()
}

func (r *admissionCommitAfterWinner) Save(ctx context.Context, work *domain.AgentWork) error {
	r.saves++
	if r.saves == 1 {
		r.completeWinner()
	}
	return r.AgentWorkRepository.Save(ctx, work)
}

// A repeat whose commit loses to the same request's winner recognizes the
// winner's start as holding its Desk and answers that admission.
func TestConcurrentContinuationRecognizesAlreadyResolvedAdmission(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	starts := 0
	started := startedWorks{AgentWorkRepository: f.sessions, onStart: func(domain.LaunchSpecification) { starts++ }}
	access := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db))
	winner := command.NewResumeWorkHandler(time.Now, started, f.agents, access, computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	admissions := &admissionCommitAfterWinner{AgentWorkRepository: started, completeWinner: func() { require.NoError(t, winner.Handle(ctx, request)) }}
	loser := command.NewResumeWorkHandler(time.Now, admissions, f.agents, access, computertest.Checked(startupAuthority{}), querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	require.NoError(t, loser.Handle(ctx, request), "a repeat that lost the commit answers the winner's admission")
	require.Equal(t, 1, starts, "only the winning repeat requests execution")
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, work.State)
}
