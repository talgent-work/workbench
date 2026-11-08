//go:build integration

package agent_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// interruptedEnvironments realize executions as realizedEnvironments do, and
// let a test act while a delivery has written or while a preparation runs,
// before the step asks its holding check again, as the realizations do
// between their remote calls.
type interruptedEnvironments struct {
	realizedEnvironments
	duringDelivery, duringPreparation atomic.Pointer[func()]
	// modelDigest is the model key a delivery would carry now; a change
	// makes the delivered credentials due.
	modelDigest atomic.Pointer[string]
	// delivered counts deliveries kept, cleared those cleared once their
	// holding check refused, and launched and withdrawn the launches and
	// withdrawals taken.
	delivered, cleared, launched, withdrawn atomic.Int32
}

// interrupt runs the pending action once.
func interrupt(action *atomic.Pointer[func()]) {
	if run := action.Swap(nil); run != nil {
		(*run)()
	}
}

func (e *interruptedEnvironments) digest() string {
	if digest := e.modelDigest.Load(); digest != nil {
		return *digest
	}
	return "model-1"
}

func (e *interruptedEnvironments) Observe(ctx context.Context, execution *domain.AgentExecution) (domain.EnvironmentObservation, error) {
	observation, err := e.realizedEnvironments.Observe(ctx, execution)
	observation.ModelDigest = e.digest()
	return observation, err
}

func (e *interruptedEnvironments) DeliverCredentials(ctx context.Context, _ *domain.AgentExecution, _ command.CredentialSupply, holding command.EnvironmentHolding) (domain.CredentialDelivery, error) {
	if err := holding(ctx); err != nil {
		return domain.CredentialDelivery{}, err
	}
	digest := e.digest()
	interrupt(&e.duringDelivery)
	if err := holding(ctx); err != nil {
		e.cleared.Add(1)
		return domain.CredentialDelivery{}, err
	}
	e.delivered.Add(1)
	return domain.CredentialDelivery{Digest: digest}, nil
}

func (e *interruptedEnvironments) WithdrawCredentials(context.Context, *domain.AgentExecution) error {
	e.withdrawn.Add(1)
	return nil
}

func (e *interruptedEnvironments) Prepare(ctx context.Context, _ *domain.AgentExecution, holding command.EnvironmentHolding) (domain.PreparationReport, error) {
	if err := holding(ctx); err != nil {
		return domain.PreparationReport{}, err
	}
	interrupt(&e.duringPreparation)
	return domain.PreparationReport{}, nil
}

func (e *interruptedEnvironments) Launch(ctx context.Context, _ *domain.AgentExecution, holding command.EnvironmentHolding) (string, error) {
	if err := holding(ctx); err != nil {
		return "", err
	}
	e.launched.Add(1)
	return launchedProcess, nil
}

// awaitExitRequest waits, off the test's goroutine, until the Work's
// execution holds an exit request.
func awaitExitRequest(t *testing.T, ctx context.Context, cloud *oneRoomCloud, workID string) {
	executions, _ := cloud.runs.parts()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		execution, err := executions.Latest(ctx, workID)
		if err != nil && !errors.Is(err, domain.ErrExecutionNotFound) {
			t.Errorf("read the execution: %v", err)
			return
		}
		if err == nil && execution.ExitRequest.Requested() {
			return
		}
	}
	t.Errorf("no exit request reached the execution of %s", workID)
}

// A Working Work stopped through the private API while its credentials are
// replaced delivers nothing more: the delivery asks again before it is kept
// and clears what it wrote, and the credentials delivered before are
// withdrawn (Exit request ends delivery, Credential withdrawal).
func TestAStopAdmittedMidDeliveryDeliversNothingAndWithdrawsCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	environments := &interruptedEnvironments{}
	cloud := startOneRoomCloudWith(t, ctx, environments)
	id, err := cloud.create(ctx, cloud.definition(t, ctx), "work-mid-delivery")
	require.NoError(t, err)
	cloud.runs.awaitLaunched(t, ctx, id)
	cloud.runs.ready(t, ctx, id)
	require.Equal(t, int32(1), environments.delivered.Load(), "the first delivery")

	stop := func() {
		if _, err := cloud.client.StopWork(sessionControlRequest(ctx), &agentprivatev1.StopWorkRequest{SubjectId: "employee-a", WorkId: id, RequestId: uuid.NewString()}); err != nil {
			t.Errorf("stop the Work: %v", err)
			return
		}
		awaitExitRequest(t, ctx, cloud, id)
	}
	environments.duringDelivery.Store(&stop)
	rotated := "model-2"
	environments.modelDigest.Store(&rotated)
	require.NoError(t, cloud.recovery.WakeExecution(ctx, cloud.runs.latest(t, ctx, id).ID), "the rotated model key makes the credentials due")

	withdrawn := cloud.runs.await(t, ctx, id, func(e *domain.AgentExecution) bool { return e.Credentials.Withdrawn }, "the delivered credentials are withdrawn")
	require.Equal(t, int32(1), environments.cleared.Load(), "the interrupted delivery is cleared")
	require.Equal(t, "model-1", withdrawn.Credentials.Digest, "the interrupted delivery is never recorded")
	require.Positive(t, environments.withdrawn.Load())
	require.Never(t, func() bool { return environments.delivered.Load() > 1 }, time.Second, 50*time.Millisecond, "nothing is delivered after the exit request")
}

// An exit request the Work's execution receives while its environment is
// prepared launches no Harness, and the credentials delivered before it are
// withdrawn (Exit request ends delivery, Credential withdrawal). A Starting
// Work admits no stop through the API; the fixture records the Work's request
// on the execution, as its Work's stop request does.
func TestAnExitRequestDuringPreparationLaunchesNothingAndWithdrawsCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	environments := &interruptedEnvironments{}
	cloud := startOneRoomCloudWith(t, ctx, environments)
	var workID atomic.Value
	requestExit := func() {
		id := workID.Load().(string)
		executions, _ := cloud.runs.parts()
		for range 20 {
			execution, err := executions.Latest(ctx, id)
			if err != nil {
				t.Errorf("read the execution: %v", err)
				return
			}
			if _, err := execution.RequestExit(domain.WorkStopRequested{WorkID: id, StartAdmittedAt: execution.StartAdmittedAt, StopRequestID: uuid.NewString()}); err != nil {
				t.Errorf("request the exit: %v", err)
				return
			}
			if err = executions.Save(ctx, execution); !errors.Is(err, domain.ErrConcurrentModification) {
				if err != nil {
					t.Errorf("save the exit request: %v", err)
				}
				return
			}
		}
		t.Errorf("the execution of %s kept changing", id)
	}
	environments.duringPreparation.Store(&requestExit)
	id, err := cloud.create(ctx, cloud.definition(t, ctx), "work-mid-preparation")
	require.NoError(t, err)
	workID.Store(id)

	withdrawn := cloud.runs.await(t, ctx, id, func(e *domain.AgentExecution) bool { return e.Credentials.Withdrawn }, "the delivered credentials are withdrawn")
	require.True(t, withdrawn.ExitRequest.Requested())
	require.Equal(t, int32(1), environments.delivered.Load(), "the credentials were delivered before the exit request")
	require.Positive(t, environments.withdrawn.Load())
	require.Never(t, func() bool { return environments.launched.Load() > 0 }, time.Second, 50*time.Millisecond, "no Harness is launched after the exit request")
	require.Empty(t, cloud.runs.latest(t, ctx, id).Process.ID, "no process is recorded")
}
