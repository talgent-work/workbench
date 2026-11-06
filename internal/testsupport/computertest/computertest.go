// Package computertest stands in for Computer's admission contract in Agent
// module tests that do not exercise admission itself.
package computertest

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// CloudID is the one Cloud the stand-in admits on.
const CloudID = "01a19bc3-075a-7c57-b42b-7fdf73b611ed"

// cloud lists the stand-in's one Cloud.
type cloud struct{}

func (cloud) ListUsableComputers(context.Context, string) ([]command.UsableComputer, error) {
	return []command.UsableComputer{{ID: CloudID, Cloud: true}}, nil
}

func (cloud) ListOpenDesks(context.Context, time.Time, string) ([]command.OpenDesk, error) {
	return nil, nil
}

// DeskSeated reports every Desk seated: the stand-in never queues a run.
func (cloud) DeskSeated(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, true, nil
}

// Admission seats every run on its Cloud, or rejects it as full while full,
// and records the Desks that left.
type Admission struct {
	cloud
	mu   sync.Mutex
	full bool
	open map[string]string
}

var _ command.ComputerAdmissionPort = (*Admission)(nil)

// SetFull makes the Cloud reject every run as full, or seat them again.
func (a *Admission) SetFull(full bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.full = full
}

func (a *Admission) AdmitRun(_ context.Context, run command.RunAdmission) (command.ComputerAnswer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.full {
		return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: domain.ErrCapacityUnavailable}, nil
	}
	if a.open == nil {
		a.open = make(map[string]string)
	}
	a.open[run.DeskID] = run.WorkID
	return command.ComputerAnswer{Kind: command.ComputerSeated}, nil
}

func (a *Admission) LeaveDesk(_ context.Context, deskID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.open, deskID)
	return nil
}

func (a *Admission) ReleaseUnusedDesk(ctx context.Context, deskID string) {
	_ = a.LeaveDesk(ctx, deskID)
}

// Open reports how many Desks are held now.
func (a *Admission) Open() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.open)
}

// Option composes the stand-in into an Agent module graph.
func Option(admission *Admission) fx.Option {
	return fx.Options(
		fx.Supply(agent.ComputerConfig{Address: "http://computer.invalid", Token: "computer"}),
		fx.Decorate(func(command.ComputerAdmissionPort) command.ComputerAdmissionPort { return admission }),
	)
}

// RoomCheck is a test's own admission decision: nil seats the run, an error
// rejects it.
type RoomCheck interface {
	AdmitRoom(context.Context) error
}

// Checked admits each run on the Cloud by check: domain.ErrCapacityUnavailable
// rejects it as full, and any other error fails the admission request.
func Checked(check RoomCheck) command.ComputerAdmissionPort {
	return checked{check: check}
}

type checked struct {
	cloud
	check RoomCheck
}

func (c checked) AdmitRun(ctx context.Context, _ command.RunAdmission) (command.ComputerAnswer, error) {
	err := c.check.AdmitRoom(ctx)
	switch {
	case err == nil:
		return command.ComputerAnswer{Kind: command.ComputerSeated}, nil
	case errors.Is(err, domain.ErrCapacityUnavailable):
		return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: err}, nil
	default:
		return command.ComputerAnswer{}, err
	}
}

func (checked) LeaveDesk(context.Context, string) error { return nil }

func (checked) ReleaseUnusedDesk(context.Context, string) {}
