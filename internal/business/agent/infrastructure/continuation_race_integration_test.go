//go:build integration

package infrastructure_test

import (
	"context"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"testing"
	"time"

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
	command.StartAdmissions
	lookups        int
	completeWinner func()
}

func (r *admissionReadAfterWinner) FindAdmittedStart(ctx context.Context, requestID string) (agentquery.AdmittedStart, bool, error) {
	result, found, err := r.StartAdmissions.FindAdmittedStart(ctx, requestID)
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
	factory := infrastructure.NewExecutionRequestsFactory(startupExecutionPort{onStart: func(domain.StartExecutionRequest) error { starts++; return nil }})
	receipts := infrastructure.NewContinuationRequests(f.db, time.Now)
	access := infrastructure.NewWorkAuthorizer(f.db)
	winner := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access, computertest.Checked(startupAuthority{}), factory, f.sessions, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	admissions := &admissionReadAfterWinner{StartAdmissions: f.sessions, completeWinner: func() { require.NoError(t, winner.Handle(ctx, request)) }}
	loser := command.NewResumeWorkHandler(time.Now, f.sessions, f.agents, access, computertest.Checked(startupAuthority{}), factory, admissions, receipts, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	require.NoError(t, loser.Handle(ctx, request), "a stale negative admission read cannot turn a completed identical request into a conflict")
	require.Equal(t, 1, starts, "only the winning retry requests execution")
	retained, found, err := receipts.GetContinuation(ctx, id)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, retained.Resolved)
	require.Empty(t, retained.Rejection)
	admitted, found, err := f.sessions.FindAdmittedStart(ctx, request.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, id, admitted.WorkID)
}
