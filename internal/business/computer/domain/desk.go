package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

// DeskState is a Desk's lifecycle state.
type DeskState string

const (
	DeskStateWaiting  DeskState = "waiting"
	DeskStateOccupied DeskState = "occupied"
	DeskStateVacated  DeskState = "vacated"
)

// Desk is one run of one Work holding, or waiting for, room on its Computer.
// It waits, is seated and leaves once, so its room returns once.
type Desk struct {
	ID         string    `validate:"required,uuid"`
	WorkID     string    `validate:"required,max=36"`
	ComputerID string    `validate:"required,uuid"`
	Demand     Room      `validate:"-"`
	State      DeskState `validate:"oneof=waiting occupied vacated"`
	// Since is when it began waiting or was seated.
	Since time.Time `validate:"required"`
	// OpenedAt is when its run was admitted.
	OpenedAt time.Time `validate:"required"`
	Events   EventCollection
	Version  int
}

// openDesk opens a Desk for an admitted run, Waiting or Occupied as its
// Computer decided.
func openDesk(run RunAdmission, computerID string, state DeskState) (*Desk, error) {
	desk := &Desk{ID: strings.TrimSpace(run.DeskID), WorkID: strings.TrimSpace(run.WorkID), ComputerID: computerID, Demand: run.Demand, State: state, Since: run.Now.UTC(), OpenedAt: run.Now.UTC(), Events: NewEventCollection()}
	if err := desk.Validate(); err != nil {
		return nil, err
	}
	if state == DeskStateOccupied {
		desk.record(DeskOccupied{DeskID: desk.ID, WorkID: desk.WorkID})
	}
	return desk, nil
}

func (d *Desk) Validate() error {
	if err := domainValidator.Struct(d); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDesk, err)
	}
	return d.Demand.Validate()
}

// Serves reports whether this Desk is the one opened for workID.
func (d *Desk) Serves(workID string) bool { return d.WorkID == workID }

// TakeSeat takes the room its Computer assigned to it. A Desk already seated
// keeps its seat.
func (d *Desk) TakeSeat(now time.Time) error {
	switch d.State {
	case DeskStateOccupied:
		return nil
	case DeskStateWaiting:
		d.State, d.Since = DeskStateOccupied, now.UTC()
		d.record(DeskOccupied{DeskID: d.ID, WorkID: d.WorkID})
		return nil
	default:
		return fmt.Errorf("%w: a vacated Desk takes no seat", ErrInvalidDesk)
	}
}

// Leave vacates the Desk once its run's process has ended and its compute is
// released, its seated run can no longer start, or its waiting run is
// withdrawn. Leaving again does nothing.
func (d *Desk) Leave() {
	if d.State == DeskStateVacated {
		return
	}
	seated := d.State == DeskStateOccupied
	d.State = DeskStateVacated
	d.record(DeskVacated{DeskID: d.ID, ComputerID: d.ComputerID, WorkID: d.WorkID, Demand: d.Demand, Seated: seated})
}

func (d *Desk) record(event Event) {
	if d.Events == nil {
		d.Events = NewEventCollection()
	}
	d.Events.Add(event)
}

// versionAtLeast reports whether a reported release is at least minimum. No
// minimum admits any; a release that cannot be read is below every minimum.
func versionAtLeast(reported, minimum string) bool {
	if strings.TrimSpace(minimum) == "" {
		return true
	}
	have, err := semver.NewVersion(reported)
	if err != nil {
		return false
	}
	want, err := semver.NewVersion(minimum)
	if err != nil {
		return false
	}
	return !have.LessThan(want)
}
