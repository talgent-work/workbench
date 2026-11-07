package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type thinkingStore struct {
	command.AgentWorkRepository
	work    domain.AgentWork
	saveErr error
	saves   int
	onSave  func() error
}

func (s *thinkingStore) Get(context.Context, string) (*domain.AgentWork, error) {
	work := s.work
	return &work, nil
}
func (s *thinkingStore) Save(_ context.Context, work *domain.AgentWork) error {
	s.saves++
	if s.onSave != nil {
		if err := s.onSave(); err != nil {
			return err
		}
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	s.work = *work
	return nil
}

type thinkingAccess struct {
	err     error
	subject string
}

func (a *thinkingAccess) AuthorizeWork(_ context.Context, subject, _ string) error {
	a.subject = subject
	return a.err
}

type thinkingHarness struct {
	command.RunningHarnessPort
	calls []string
	err   error
}

func (h *thinkingHarness) SetThinkingLevel(_ context.Context, _ string, level string) error {
	h.calls = append(h.calls, level)
	return h.err
}

func thinkingApplication() (*application.Application, *thinkingStore, *thinkingAccess, *thinkingHarness) {
	model := domain.WorkModelSelection{ConfigurationID: "model"}
	store := &thinkingStore{work: domain.AgentWork{ID: "work", State: domain.WorkWorking, ThinkingLevel: "low", WorkModel: model, HarnessProcess: &domain.HarnessProcess{}}}
	auth := &thinkingAccess{}
	harness := &thinkingHarness{}
	return &application.Application{Works: store, WorkPrincipals: auth, Harness: harness, Models: query.NewModelConfigurations(query.WorkModelOption{Selection: model, ThinkingLevels: []string{"low", "high"}, DefaultThinkingLevel: "high"})}, store, auth, harness
}

func TestThinkingLevelReadSurvivesWorkLifecycle(t *testing.T) {
	for _, state := range []domain.WorkState{domain.WorkStarting, domain.WorkWorking, domain.WorkStopping, domain.WorkStopped, domain.WorkFailed, domain.WorkClosing, domain.WorkClosed} {
		t.Run(string(state), func(t *testing.T) {
			app, store, auth, harness := thinkingApplication()
			store.work.State = state
			app.Harness = nil // Reading must not contact an execution, even if it is unavailable.
			level, err := app.GetThinkingLevel(context.Background(), application.ControlWork{ActorUserID: "reader", WorkID: "work"})
			require.NoError(t, err)
			require.Equal(t, "low", level)
			require.Equal(t, "reader", auth.subject, "only the owner reads its Work")
			require.Empty(t, harness.calls)
			require.Zero(t, store.saves)
		})
	}
}

func TestThinkingLevelChangePersistsOnlyAcceptedSetting(t *testing.T) {
	for _, test := range []struct {
		name, level, want           string
		state                       domain.WorkState
		nativeErr, saveErr, wantErr error
	}{
		{name: "working", level: "high", want: "high", state: domain.WorkWorking},
		{name: "default", level: "", want: "high", state: domain.WorkWorking},
		{name: "stopped is read only", level: "high", want: "low", state: domain.WorkStopped, wantErr: domain.ErrWorkTransition},
		{name: "unsupported", level: "max", want: "low", state: domain.WorkWorking, wantErr: domain.ErrInvalidWork},
		{name: "harness rejection", level: "high", want: "low", state: domain.WorkWorking, nativeErr: domain.ErrExecutionRequest, wantErr: domain.ErrExecutionRequest},
		{name: "storage conflict is explicit", level: "high", want: "low", state: domain.WorkWorking, saveErr: domain.ErrConcurrentModification, wantErr: domain.ErrConcurrentModification},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, store, auth, harness := thinkingApplication()
			store.work.State, store.saveErr, harness.err = test.state, test.saveErr, test.nativeErr
			err := app.SetThinkingLevel(context.Background(), application.SetThinkingLevel{ActorUserID: "writer", WorkID: "work", Level: test.level})
			require.ErrorIs(t, err, test.wantErr)
			require.Equal(t, test.want, store.work.ThinkingLevel)
			require.Equal(t, "writer", auth.subject, "only the owner changes its Work")
			if test.wantErr == nil {
				require.Equal(t, []string{test.want}, harness.calls)
				require.Equal(t, 1, store.saves)
			}
			if errors.Is(test.wantErr, domain.ErrWorkTransition) || errors.Is(test.wantErr, domain.ErrInvalidWork) {
				require.Empty(t, harness.calls)
				require.Zero(t, store.saves)
			}
			if test.nativeErr != nil {
				require.Zero(t, store.saves)
			}
		})
	}
}

func TestThinkingLevelRequiresTheWorkOwner(t *testing.T) {
	app, store, auth, harness := thinkingApplication()
	auth.err = access.ErrAccessDenied
	level, err := app.GetThinkingLevel(context.Background(), application.ControlWork{ActorUserID: "outsider", WorkID: "work"})
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.Empty(t, level)
	require.ErrorIs(t, app.SetThinkingLevel(context.Background(), application.SetThinkingLevel{ActorUserID: "outsider", WorkID: "work", Level: "high"}), access.ErrAccessDenied)
	require.Empty(t, harness.calls)
	require.Zero(t, store.saves)
}

func TestThinkingLevelPersistenceKeepsConcurrentLifecycleWithoutReplayingControl(t *testing.T) {
	app, store, _, harness := thinkingApplication()
	store.onSave = func() error {
		if store.saves == 1 {
			store.work.State = domain.WorkStopped
			return domain.ErrConcurrentModification
		}
		return nil
	}
	require.NoError(t, app.SetThinkingLevel(context.Background(), application.SetThinkingLevel{ActorUserID: "writer", WorkID: "work", Level: "high"}))
	require.Equal(t, domain.WorkStopped, store.work.State)
	require.Equal(t, "high", store.work.ThinkingLevel)
	require.Equal(t, []string{"high"}, harness.calls)
	require.Equal(t, 2, store.saves)
}

func TestThinkingLevelPersistenceDoesNotReplaceCompetingSetting(t *testing.T) {
	app, store, _, harness := thinkingApplication()
	store.onSave = func() error { store.work.ThinkingLevel = "max"; return domain.ErrConcurrentModification }
	require.ErrorIs(t, app.SetThinkingLevel(context.Background(), application.SetThinkingLevel{ActorUserID: "writer", WorkID: "work", Level: "high"}), domain.ErrConcurrentModification)
	require.Equal(t, "max", store.work.ThinkingLevel)
	require.Equal(t, []string{"high"}, harness.calls)
	require.Equal(t, 1, store.saves)
}

// The machine's Claude login has no Model Template: every Harness level is
// offered, whichever Model Configurations are provided.
func TestAMachineLoginWorkOffersEveryHarnessThinkingLevel(t *testing.T) {
	app, store, _, harness := thinkingApplication()
	store.work.WorkModel = domain.MachineLoginModel()
	require.NoError(t, app.SetThinkingLevel(context.Background(), application.SetThinkingLevel{ActorUserID: "owner", WorkID: "work", Level: "max"}))
	require.Equal(t, []string{"max"}, harness.calls)
	require.Equal(t, "max", store.work.ThinkingLevel)
	require.ErrorIs(t, app.SetThinkingLevel(context.Background(), application.SetThinkingLevel{ActorUserID: "owner", WorkID: "work", Level: "extreme"}), domain.ErrInvalidWork)
}
