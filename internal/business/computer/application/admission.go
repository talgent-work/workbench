package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

// Answer is a Computer's answer to one run.
type Answer string

const (
	AnswerSeated   Answer = "seated"
	AnswerWaiting  Answer = "waiting"
	AnswerRejected Answer = "rejected"
)

// AdmitRun asks a Computer to admit one run of a Work under a Desk identity
// the caller chose.
type AdmitRun struct {
	ComputerID  string
	DeskID      string
	WorkID      string
	WorkOwnerID string
	StarterID   string
	// Sandbox asks for Claude Code's sandbox.
	Sandbox bool
}

// Admission is the answer to one run; Rejection says why it was refused.
type Admission struct {
	Answer    Answer
	Rejection domain.Rejection
	// Kind is what the answering Computer is, so its caller learns whether
	// the run is on a Cloud or a Managed Computer.
	Kind domain.Kind
}

// ErrDeskLeft refuses to admit again a run whose Desk already left.
var ErrDeskLeft = errors.New("computer: the run's desk already left")

// AdmitRun answers one run. Admission and the Desk it opens commit together,
// so two runs never claim the same room; repeating a Desk identity returns
// that Desk's current answer.
func (a *Application) AdmitRun(ctx context.Context, request AdmitRun) (Admission, error) {
	if answer, found, err := a.existingAnswer(ctx, request); found || err != nil {
		return answer, err
	}
	computer, err := a.computers.Get(ctx, request.ComputerID)
	if err != nil {
		return Admission{}, err
	}
	// The cluster's quota is a supplied fact, read before admission so no
	// transaction waits on the cluster.
	quotaFits := false
	if computer.Kind == domain.KindCloud {
		if quotaFits, err = a.quota.FitsRun(ctx); err != nil {
			return Admission{}, err
		}
	}
	var admission Admission
	err = retryConcurrent(ctx, func() error {
		return a.transactions.Within(ctx, func(ctx context.Context) error {
			computer, err := a.computers.Get(ctx, request.ComputerID)
			if err != nil {
				return err
			}
			desk, rejection, err := computer.AdmitRun(domain.RunAdmission{
				DeskID: request.DeskID, WorkID: request.WorkID, WorkOwnerID: request.WorkOwnerID, StarterID: request.StarterID, Sandbox: request.Sandbox,
				Demand: computer.RunDemand(a.cloud.RunDemand), QuotaFits: quotaFits, MinimumVersion: a.minimum, Now: a.now(),
			})
			if err != nil {
				return err
			}
			if rejection != "" {
				admission = Admission{Answer: AnswerRejected, Rejection: rejection, Kind: computer.Kind}
				return nil
			}
			if err := a.desks.Save(ctx, desk); err != nil {
				return err
			}
			admission = Admission{Answer: answerOf(desk), Kind: computer.Kind}
			return a.computers.Save(ctx, computer)
		})
	})
	if errors.Is(err, domain.ErrDeskTaken) {
		// A concurrent repeat of this request opened the Desk first.
		answer, found, lookupErr := a.existingAnswer(ctx, request)
		if lookupErr != nil || found {
			return answer, lookupErr
		}
	}
	return admission, err
}

func (a *Application) existingAnswer(ctx context.Context, request AdmitRun) (Admission, bool, error) {
	desk, err := a.desks.Get(ctx, request.DeskID)
	if errors.Is(err, domain.ErrDeskNotFound) {
		return Admission{}, false, nil
	}
	if err != nil {
		return Admission{}, false, err
	}
	if !desk.Serves(request.WorkID) || desk.ComputerID != request.ComputerID {
		return Admission{}, true, domain.ErrDeskTaken
	}
	if desk.State == domain.DeskStateVacated {
		return Admission{}, true, ErrDeskLeft
	}
	computer, err := a.computers.Get(ctx, desk.ComputerID)
	if err != nil {
		return Admission{}, true, err
	}
	return Admission{Answer: answerOf(desk), Kind: computer.Kind}, true, nil
}

func answerOf(desk *domain.Desk) Answer {
	if desk.State == domain.DeskStateWaiting {
		return AnswerWaiting
	}
	return AnswerSeated
}

// LeaveDesk vacates a run's Desk once; its Computer takes back its room, or
// its place in line, and seats the waiting Desks room now allows. Leaving an
// unknown or vacated Desk does nothing.
func (a *Application) LeaveDesk(ctx context.Context, deskID string) error {
	var seated []SeatedDesk
	err := retryConcurrent(ctx, func() error {
		seated = nil
		return a.transactions.Within(ctx, func(ctx context.Context) error {
			desk, err := a.desks.Get(ctx, deskID)
			if errors.Is(err, domain.ErrDeskNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			desk.Leave()
			vacated, ok := lastVacated(desk.Events.Drain())
			if !ok {
				return nil
			}
			computer, err := a.computers.Get(ctx, desk.ComputerID)
			if err != nil {
				return err
			}
			if err := computer.FreeDesk(vacated); err != nil {
				return err
			}
			if err := a.applyCloudRules(computer); err != nil {
				return err
			}
			if err := a.desks.Save(ctx, desk); err != nil {
				return err
			}
			if err := a.computers.Save(ctx, computer); err != nil {
				return err
			}
			seated, err = a.seatAssigned(ctx, computer.Events.Drain(), a.now())
			return err
		})
	})
	if err == nil {
		a.seatings.Publish(ctx, seated)
	}
	return err
}

// SeatedDesk is a waiting Desk its Computer seated: Desk Assigned, as Agent
// consumes it.
type SeatedDesk struct {
	ComputerID string
	DeskID     string
	WorkID     string
	SeatedAt   time.Time
}

// Seatings publishes Desk Assigned for each seated waiting Desk after its
// commit, best effort: Agent also asks for the Desks its Works wait on, so a
// lost publication only delays the start.
type Seatings interface {
	Publish(ctx context.Context, seated []SeatedDesk)
}

type noSeatings struct{}

func (noSeatings) Publish(context.Context, []SeatedDesk) {}

// seatAssigned seats each Desk its Computer assigned room to, in the same
// commit as the room it frees (Room consistency).
func (a *Application) seatAssigned(ctx context.Context, events []domain.Event, now time.Time) ([]SeatedDesk, error) {
	var seated []SeatedDesk
	for _, event := range events {
		assigned, ok := event.(domain.DeskAssigned)
		if !ok {
			continue
		}
		desk, err := a.desks.Get(ctx, assigned.DeskID)
		if err != nil {
			return nil, fmt.Errorf("seat assigned desk: %w", err)
		}
		if err := desk.TakeSeat(now); err != nil {
			return nil, err
		}
		if err := a.desks.Save(ctx, desk); err != nil {
			return nil, err
		}
		seated = append(seated, SeatedDesk{ComputerID: assigned.ComputerID, DeskID: desk.ID, WorkID: desk.WorkID, SeatedAt: desk.Since})
	}
	return seated, nil
}

// DeskStanding is where one Desk stands now.
type DeskStanding struct {
	State domain.DeskState
	// SeatedAt is when its run was seated, set while it is occupied.
	SeatedAt time.Time
}

// Desk says where one Desk stands now; a Desk never opened is not found.
func (a *Application) Desk(ctx context.Context, deskID string) (DeskStanding, error) {
	desk, err := a.desks.Get(ctx, deskID)
	if err != nil {
		return DeskStanding{}, err
	}
	standing := DeskStanding{State: desk.State}
	if desk.State == domain.DeskStateOccupied {
		standing.SeatedAt = desk.Since
	}
	return standing, nil
}

func lastVacated(events []domain.Event) (domain.DeskVacated, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if vacated, ok := events[i].(domain.DeskVacated); ok {
			return vacated, true
		}
	}
	return domain.DeskVacated{}, false
}

// UsableComputer is one Computer a person may choose.
type UsableComputer struct {
	ID   string
	Kind domain.Kind
	Name string
	// Reachable is whether the platform reaches it now; a Cloud always is.
	Reachable bool
	// Sandbox is whether a Managed Computer's machine last reported that it
	// can provide Claude Code's sandbox; a Cloud provides none.
	Sandbox bool
}

// ListUsableComputers lists every Cloud, then the person's own Managed
// Computers.
func (a *Application) ListUsableComputers(ctx context.Context, personID string) ([]UsableComputer, error) {
	return a.queries.ListUsableComputers(ctx, personID)
}

// OpenDesk is one waiting or occupied Desk.
type OpenDesk struct {
	DeskID string
	WorkID string
}

// maxDeskPage bounds one page of open Desks.
const maxDeskPage = 500

// ListOpenDesks pages through open Desks opened before a time.
func (a *Application) ListOpenDesks(ctx context.Context, openedBefore time.Time, afterDeskID string, limit int) ([]OpenDesk, error) {
	if limit <= 0 || limit > maxDeskPage {
		limit = maxDeskPage
	}
	return a.queries.ListOpenDesks(ctx, openedBefore, afterDeskID, limit)
}
