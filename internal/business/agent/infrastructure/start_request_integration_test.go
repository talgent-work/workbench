//go:build integration

package infrastructure_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// commitBarrier pauses a real start admission just before it commits, so a
// concurrent end of the same start request races the admission transaction.
type commitBarrier struct {
	*infrastructure.AgentWorkRepository
	arrived, proceed chan struct{}
	once             *sync.Once
}

func newCommitBarrier(sessions *infrastructure.AgentWorkRepository) commitBarrier {
	return commitBarrier{AgentWorkRepository: sessions, arrived: make(chan struct{}), proceed: make(chan struct{}), once: &sync.Once{}}
}

func (b commitBarrier) CommitStart(ctx context.Context, work *domain.AgentWork, start agentquery.AdmittedStart, requestID string) error {
	b.once.Do(func() { close(b.arrived) })
	select {
	case <-b.proceed:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.AgentWorkRepository.CommitStart(ctx, work, start, requestID)
}

// Ending a start request whose admission is in flight
// prevents that Resume admission without changing the stopped conversation.
func TestEndingStartRequestPreventsAdmissionWithoutClosingConversation(t *testing.T) {
	f, id := stoppedContinuationSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, agents, sessions := f.db, f.agents, f.sessions
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	before, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	var starts atomic.Int32
	factory := startupExecutionPort{onStart: func(domain.StartExecutionRequest) error { starts.Add(1); return nil }}
	request := command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}
	end := command.NewEndWorkExecutionHandler(sessions, auth, factory)
	barrier := newCommitBarrier(sessions)
	var release sync.Once
	unpause := func() { release.Do(func() { close(barrier.proceed) }) }
	defer unpause()
	result := make(chan error, 1)
	go func() {
		result <- command.NewResumeWorkHandler(time.Now, barrier, agents, auth, computertest.Checked(startupAuthority{}), factory, infrastructure.NewContinuationRequests(db, time.Now), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, request)
	}()
	select {
	case <-barrier.arrived:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, end.Handle(ctx, command.EndWorkExecution{SubjectID: "owner", WorkID: id, StartRequestID: request.RequestID, RequestID: "end"}))
	unpause()
	require.ErrorIs(t, <-result, command.ErrStartRequestCanceled)
	require.Zero(t, starts.Load())
	saved, err := sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.WorkStopped, saved.State)
	require.Equal(t, before.HarnessProcess, saved.HarnessProcess)
	require.False(t, saved.HoldsProcess(), "a canceled start never holds a Harness process")
}
