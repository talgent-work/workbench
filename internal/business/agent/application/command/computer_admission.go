package command

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// RunAdmission asks a Work's Computer to admit one run under the Desk
// identity Agent chose for it.
type RunAdmission struct {
	ComputerID  string
	DeskID      string
	WorkID      string
	WorkOwnerID string
	StarterID   string
	// Sandbox asks for Claude Code's sandbox, which only a machine that can
	// provide it admits.
	Sandbox bool
}

// ComputerAnswerKind is what a Computer answered one run.
type ComputerAnswerKind string

const (
	ComputerSeated   ComputerAnswerKind = "seated"
	ComputerWaiting  ComputerAnswerKind = "waiting"
	ComputerRejected ComputerAnswerKind = "rejected"
)

// ComputerAnswer is a Computer's answer to one run; a rejection carries its
// reason as a domain error.
type ComputerAnswer struct {
	Kind      ComputerAnswerKind
	Rejection error
}

// UsableComputer is one Computer a person may start a Work on.
type UsableComputer struct {
	ID    string
	Cloud bool
}

// ComputerAdmissionPort is Computer's admission contract as Agent uses it.
// Each run of a Work takes a Desk before it starts and Agent reports the
// Desk's departure.
type ComputerAdmissionPort interface {
	// ListUsableComputers lists every Cloud, then the person's own Managed
	// Computers.
	ListUsableComputers(ctx context.Context, personID string) ([]UsableComputer, error)
	AdmitRun(ctx context.Context, run RunAdmission) (ComputerAnswer, error)
	// LeaveDesk reports a Desk's departure; repeating it does nothing.
	LeaveDesk(ctx context.Context, deskID string) error
	// ReleaseUnusedDesk reports, without failing its caller, the departure of
	// a Desk whose run its Work never took up; an unreported one is left to
	// the open-Desk reconciliation.
	ReleaseUnusedDesk(ctx context.Context, deskID string)
	// ListOpenDesks pages Desks still held that were opened before a time.
	ListOpenDesks(ctx context.Context, openedBefore time.Time, afterDeskID string) ([]OpenDesk, error)
	// DeskSeated reports whether a Desk is seated now and, when Computer
	// says, when it was seated, so a waiting start whose Desk Assigned was
	// lost still begins; a vacated or unknown Desk is not seated.
	DeskSeated(ctx context.Context, deskID string) (seatedAt time.Time, seated bool, err error)
}

// OpenDesk is a Desk its Computer still counts as waiting or occupied.
type OpenDesk struct {
	DeskID string
	WorkID string
}

// ErrComputerUnavailable reports that the Work's Computer could not be asked.
var ErrComputerUnavailable = errors.New("agent: the Work's Computer cannot be reached for admission")

// workComputer is the Computer a new Work starts on: the one its starter
// chose among the Computers usable to them, or, with none chosen, the first
// Cloud usable to them.
func workComputer(ctx context.Context, computers ComputerAdmissionPort, personID, chosen string) (UsableComputer, error) {
	usable, err := computers.ListUsableComputers(ctx, personID)
	if err != nil {
		return UsableComputer{}, err
	}
	chosen = strings.TrimSpace(chosen)
	for _, computer := range usable {
		if (chosen == "" && computer.Cloud) || (chosen != "" && computer.ID == chosen) {
			return computer, nil
		}
	}
	if chosen != "" {
		return UsableComputer{}, domain.ErrComputerNotUsable
	}
	return UsableComputer{}, ErrComputerUnavailable
}

// admitRun asks the Work's Computer to admit one run: it is seated, or
// waits in the Computer's line when the Computer queues a run while full.
func admitRun(ctx context.Context, computers ComputerAdmissionPort, run RunAdmission) (domain.ComputerSeat, error) {
	run.DeskID = uuid.Must(uuid.NewV7()).String()
	answer, err := computers.AdmitRun(ctx, run)
	if err != nil {
		return domain.ComputerSeat{}, err
	}
	switch answer.Kind {
	case ComputerSeated, ComputerWaiting:
		return domain.ComputerSeat{ComputerID: run.ComputerID, DeskID: run.DeskID, Waiting: answer.Kind == ComputerWaiting}, nil
	default:
		if answer.Rejection != nil {
			return domain.ComputerSeat{}, answer.Rejection
		}
		return domain.ComputerSeat{}, domain.ErrCapacityUnavailable
	}
}
