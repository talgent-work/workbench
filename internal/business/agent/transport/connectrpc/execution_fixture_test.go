package connectrpc_test

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// launchedProcess is the process every realized execution of these fixtures
// launches.
const launchedProcess = "process"

// syncEvents hands each Domain Event to its subscribers at once, so the
// Policies a saved change triggers have run when its save returns.
type syncEvents struct {
	mu       sync.Mutex
	handlers []domainevent.Handler
}

func (d *syncEvents) subscribe(handler domainevent.Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers = append(d.handlers, handler)
}

func (d *syncEvents) Dispatch(ctx context.Context, event domainevent.Event) error {
	d.mu.Lock()
	handlers := slices.Clone(d.handlers)
	d.mu.Unlock()
	for _, handler := range handlers {
		if slices.Contains(handler.Listening(), event.Kind()) {
			handler.Handle(ctx, event)
		}
	}
	return nil
}

func (d *syncEvents) DispatchAll(ctx context.Context, events []domainevent.Event) error {
	for _, event := range events {
		if err := d.Dispatch(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (*syncEvents) Close(context.Context) error { return nil }

// rpcExecutions keeps AgentExecutions in memory under an optimistic version
// and dispatches the Domain Events of each save. An execution seated at its
// beginning is realized at once, standing for its environment's steps: its
// creation is submitted, its environment bound and launchedProcess launched.
type rpcExecutions struct {
	mu     sync.Mutex
	saved  map[string]domain.AgentExecution
	order  []string
	events domainevent.Dispatcher
	now    func() time.Time
}

func (r *rpcExecutions) Get(_ context.Context, id string) (*domain.AgentExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved, found := r.saved[id]
	if !found {
		return nil, domain.ErrExecutionNotFound
	}
	return loadedExecution(saved), nil
}

func (r *rpcExecutions) Latest(_ context.Context, workID string) (*domain.AgentExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range slices.Backward(r.order) {
		if saved := r.saved[id]; saved.WorkID == workID {
			return loadedExecution(saved), nil
		}
	}
	return nil, domain.ErrExecutionNotFound
}

func (r *rpcExecutions) Save(ctx context.Context, execution *domain.AgentExecution) error {
	if err := execution.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.saved == nil {
		r.saved = map[string]domain.AgentExecution{}
	}
	stored := *execution
	if execution.Version == 0 {
		for _, other := range r.saved {
			if other.WorkID == execution.WorkID && !other.Retired() {
				r.mu.Unlock()
				return domain.ErrExecutionAdmission
			}
		}
		r.order = append(r.order, execution.ID)
		if !stored.Queued() && commandtest.Submit(&stored) {
			if _, err := stored.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment-" + stored.ID}); err != nil {
				r.mu.Unlock()
				return err
			}
			if _, err := stored.RecordLaunch(launchedProcess); err != nil {
				r.mu.Unlock()
				return err
			}
		}
	} else if current, found := r.saved[execution.ID]; !found || current.Version != execution.Version {
		r.mu.Unlock()
		return domain.ErrConcurrentModification
	}
	stored.Version = execution.Version + 1
	stored.Events = nil
	r.saved[execution.ID] = stored
	r.mu.Unlock()
	if execution.Events == nil || execution.Events.Len() == 0 || r.events == nil {
		return nil
	}
	return r.events.DispatchAll(ctx, execution.Events.Drain())
}

// all lists every execution begun, in order.
func (r *rpcExecutions) all() []domain.AgentExecution {
	r.mu.Lock()
	defer r.mu.Unlock()
	executions := make([]domain.AgentExecution, 0, len(r.order))
	for _, id := range r.order {
		executions = append(executions, r.saved[id])
	}
	return executions
}

// launches are the LaunchSpecifications of every execution begun.
func (r *rpcExecutions) launches() []domain.LaunchSpecification {
	var launches []domain.LaunchSpecification
	for _, execution := range r.all() {
		launches = append(launches, execution.Launch)
	}
	return launches
}

// exitRequests are the stop identities the Works asked their executions'
// processes to exit under.
func (r *rpcExecutions) exitRequests() []string {
	var requests []string
	for _, execution := range r.all() {
		if execution.ExitRequest.Requested() {
			requests = append(requests, execution.ExitRequest.StopRequestID)
		}
	}
	return requests
}

// retired counts the executions retired.
func (r *rpcExecutions) retired() int {
	count := 0
	for _, execution := range r.all() {
		if execution.Retired() {
			count++
		}
	}
	return count
}

// release confirms the release of the Work's retired execution, as its
// reconciliation does once its Desk left and it owns no environment.
func (r *rpcExecutions) release(t testing.TB, workID string) {
	t.Helper()
	ctx := context.Background()
	execution, err := r.Latest(ctx, workID)
	require.NoError(t, err)
	execution.RecordDeskLeft(r.now())
	released, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, r.now())
	require.NoError(t, err)
	require.True(t, released, "the Work's execution is retired")
	require.NoError(t, r.Save(ctx, execution))
}

func loadedExecution(saved domain.AgentExecution) *domain.AgentExecution {
	loaded := saved
	loaded.Events = domain.NewEventCollection()
	return &loaded
}

// executionWorld wires a Work's Policies with its AgentExecution: the Work's
// The execution requests begin, stop and retire its execution, and the execution's reports
// reach the Work, each as soon as the change is saved.
func executionWorld(now func() time.Time, works domain.AgentWorkRepository, computers command.ComputerPort, buffer command.DispatcherPort) (*syncEvents, *rpcExecutions, *command.ExecutionCoordination) {
	events := &syncEvents{}
	executions := &rpcExecutions{events: events, now: now}
	coordination := command.NewExecutionCoordination(now, works, commandtest.Definitions{}, executions, computers, buffer)
	logger := slog.Default()
	events.subscribe(eventhandler.NewBeginExecution(coordination, logger))
	events.subscribe(eventhandler.NewRequestProcessExit(coordination, logger))
	events.subscribe(eventhandler.NewRetireExecution(coordination, logger))
	events.subscribe(eventhandler.NewApplyExecutionReport(coordination, logger))
	events.subscribe(eventhandler.NewConfirmComputeRelease(coordination, logger))
	return events, executions, coordination
}
