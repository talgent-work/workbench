package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Kind is what a Computer is.
type Kind string

const (
	KindCloud   Kind = "cloud"
	KindManaged Kind = "managed"
)

// AdmissionPolicy is what a full Computer answers a run.
type AdmissionPolicy string

const (
	AdmissionReject AdmissionPolicy = "reject"
	AdmissionQueue  AdmissionPolicy = "queue"
)

// State is a Computer's lifecycle state.
type State string

const (
	StateActive  State = "active"
	StateRemoved State = "removed"
)

// Rejection says why a Computer refused a run; empty when it did not.
type Rejection string

const (
	RejectedFull        Rejection = "full"
	RejectedNotUsable   Rejection = "not_usable"
	RejectedUnreachable Rejection = "unreachable"
	RejectedRemoved     Rejection = "removed"
	RejectedOutdated    Rejection = "outdated"
	// RejectedNoSandbox refuses a run that asks for Claude Code's sandbox on
	// a Computer whose machine does not report that it can provide it.
	RejectedNoSandbox Rejection = "no_sandbox"
)

// Presence is whether the platform reaches a Computer now, and since when it
// does not.
type Presence struct {
	Reachable        bool
	UnreachableSince time.Time
}

// ReportedVersions are the versions a Managed Computer's machine reports.
type ReportedVersions struct {
	TalgentComputer string `validate:"max=64"`
	ClaudeCode      string `validate:"max=64"`
}

// WaitingDesk is one place in a Computer's Waiting Line.
type WaitingDesk struct {
	DeskID string
	Demand Room
}

// Computer is one place Works run, a Cloud or a Managed Computer, with the
// Desks it can seat at once. It holds the totals and the Waiting Line, not
// the Desks themselves.
type Computer struct {
	ID          string          `validate:"required,uuid"`
	Kind        Kind            `validate:"oneof=cloud managed"`
	OwnerID     string          `validate:"max=255"`
	Name        string          `validate:"required,max=100"`
	Capacity    Room            `validate:"-"`
	Occupied    Room            `validate:"-"`
	Policy      AdmissionPolicy `validate:"oneof=reject queue"`
	WaitingLine []WaitingDesk   `validate:"-"`
	Presence    Presence        `validate:"-"`
	Versions    ReportedVersions
	// Sandbox is whether a Managed Computer's machine last reported that it
	// can provide Claude Code's sandbox; a Cloud provides none.
	Sandbox bool
	// Credential is a Managed Computer's current Computer Credential.
	Credential Credential `validate:"-"`
	// PendingCredential is the successor a rotation issued and its machine
	// has not confirmed yet; RetiredCredential the credential a confirmed
	// successor replaced, valid until its ExpiresAt for CredentialOverlap.
	PendingCredential Credential `validate:"-"`
	RetiredCredential Credential `validate:"-"`
	State             State      `validate:"oneof=active removed"`
	Events            EventCollection
	Version           int
}

// NewCloud establishes the platform's Computer for one cluster. A Cloud is
// owned by the platform and always reachable while the platform runs.
func NewCloud(id, name string, capacity Room, policy AdmissionPolicy) (*Computer, error) {
	computer := &Computer{ID: strings.TrimSpace(id), Kind: KindCloud, Name: strings.TrimSpace(name), Capacity: capacity, Policy: policy, Presence: Presence{Reachable: true}, State: StateActive, Events: NewEventCollection()}
	if err := computer.Validate(); err != nil {
		return nil, err
	}
	return computer, nil
}

func (c *Computer) Validate() error {
	if err := domainValidator.Struct(c); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidComputer, err)
	}
	if err := c.Capacity.Validate(); err != nil {
		return err
	}
	if err := c.Occupied.Validate(); err != nil {
		return err
	}
	if !c.Occupied.within(c.Capacity) {
		return fmt.Errorf("%w: occupied room exceeds capacity", ErrInvalidComputer)
	}
	if (c.Kind == KindManaged) == (c.OwnerID == "") {
		return fmt.Errorf("%w: only a Managed Computer has a personal owner", ErrInvalidComputer)
	}
	if c.Kind == KindCloud && (c.Credential != (Credential{}) || c.PendingCredential != (Credential{}) || c.RetiredCredential != (Credential{})) {
		return fmt.Errorf("%w: a Cloud holds no Computer Credential", ErrInvalidComputer)
	}
	if !c.Presence.Reachable && c.Presence.UnreachableSince.IsZero() {
		return fmt.Errorf("%w: an unreachable Computer records since when", ErrInvalidComputer)
	}
	return nil
}

// SetRoomRules takes its owner's Capacity and Admission Policy. Capacity
// never drops below the room occupied now; a Managed Computer's Capacity is
// a number of Desks. Room the new Capacity adds seats the head of the
// Waiting Line, as a vacated Desk's room does.
func (c *Computer) SetRoomRules(capacity Room, policy AdmissionPolicy) error {
	if c.State == StateRemoved {
		return ErrComputerRemoved
	}
	if err := capacity.Validate(); err != nil {
		return err
	}
	if c.Kind == KindManaged && (capacity.MilliCPU != 0 || capacity.MemoryBytes != 0 || capacity.Desks < 1 || capacity.Desks > MaxManagedDesks) {
		return fmt.Errorf("%w: a Managed Computer runs 1 to %d Works at once", ErrInvalidComputer, MaxManagedDesks)
	}
	if policy != AdmissionReject && policy != AdmissionQueue {
		return fmt.Errorf("%w: unknown admission policy", ErrInvalidComputer)
	}
	if !c.Occupied.within(capacity) {
		return ErrCapacityBelowOccupied
	}
	c.Capacity, c.Policy = capacity, policy
	c.assignWaitingRoom()
	return nil
}

// RunAdmission is what one run of a Work brings to its Computer's admission.
type RunAdmission struct {
	DeskID      string
	WorkID      string
	WorkOwnerID string
	StarterID   string
	// Demand is the room the run needs, as this Computer's kind decides.
	Demand Room
	// QuotaFits reports, for a Cloud, whether its cluster's quota fits the
	// demand now.
	QuotaFits bool
	// MinimumVersion is the oldest Talgent Computer a Managed Computer may
	// take a run on.
	MinimumVersion string
	// Sandbox asks for Claude Code's sandbox, which only a machine that
	// reports it can provide admits.
	Sandbox bool
	Now     time.Time
}

// RunDemand is the room one run needs on this Computer: one Desk on a
// Managed Computer, and on a Cloud the compute its cluster gives one run.
func (c *Computer) RunDemand(cloudRun Room) Room {
	if c.Kind == KindManaged {
		return Room{Desks: 1}
	}
	return cloudRun
}

// AdmitRun admits a run: it seats the run when this Computer is usable by its
// starter, reachable, current enough, provides the sandbox a run asking for
// it needs, fits its cluster's quota for a Cloud
// and has room, adding the demand to Occupied; when full it queues the run or
// rejects it by its Admission Policy; it rejects it otherwise. A seated or
// queued run gets its Desk.
func (c *Computer) AdmitRun(run RunAdmission) (*Desk, Rejection, error) {
	if run.Now.IsZero() {
		return nil, "", fmt.Errorf("%w: admission time is required", ErrInvalidDesk)
	}
	if err := run.Demand.Validate(); err != nil {
		return nil, "", err
	}
	if rejection := c.refusal(run); rejection != "" {
		return nil, rejection, nil
	}
	if c.Kind == KindCloud && !run.QuotaFits {
		return nil, RejectedFull, nil
	}
	if c.Occupied.add(run.Demand).within(c.Capacity) {
		desk, err := openDesk(run, c.ID, DeskStateOccupied)
		if err != nil {
			return nil, "", err
		}
		c.Occupied = c.Occupied.add(run.Demand)
		return desk, "", nil
	}
	if c.Policy == AdmissionReject || !run.Demand.within(c.Capacity) {
		return nil, RejectedFull, nil
	}
	desk, err := openDesk(run, c.ID, DeskStateWaiting)
	if err != nil {
		return nil, "", err
	}
	c.WaitingLine = append(c.WaitingLine, WaitingDesk{DeskID: desk.ID, Demand: run.Demand})
	return desk, "", nil
}

func (c *Computer) refusal(run RunAdmission) Rejection {
	switch {
	case c.State == StateRemoved:
		return RejectedRemoved
	case !c.UsableBy(run.WorkOwnerID, run.StarterID):
		return RejectedNotUsable
	case !c.Presence.Reachable:
		return RejectedUnreachable
	case c.Kind == KindManaged && !versionAtLeast(c.Versions.TalgentComputer, run.MinimumVersion):
		return RejectedOutdated
	case run.Sandbox && !c.Sandbox:
		return RejectedNoSandbox
	}
	return ""
}

// UsableBy reports whether a run of a Work owned by workOwner and started by
// starter may use this Computer: every Cloud serves everyone; a Managed
// Computer runs only its owner's own Works, started by its owner.
func (c *Computer) UsableBy(workOwner, starter string) bool {
	if c.Kind == KindCloud {
		return true
	}
	return c.OwnerID != "" && workOwner == c.OwnerID && starter == c.OwnerID
}

// FreeDesk takes back a vacated Desk's room, or drops it from the Waiting
// Line, and assigns room to the head of the line while room allows.
func (c *Computer) FreeDesk(vacated DeskVacated) error {
	if vacated.ComputerID != c.ID {
		return fmt.Errorf("%w: the Desk belongs to another Computer", ErrInvalidDesk)
	}
	if vacated.Seated {
		occupied, err := c.Occupied.sub(vacated.Demand)
		if err != nil {
			return err
		}
		c.Occupied = occupied
	} else {
		c.WaitingLine = slices.DeleteFunc(c.WaitingLine, func(waiting WaitingDesk) bool { return waiting.DeskID == vacated.DeskID })
	}
	c.assignWaitingRoom()
	return nil
}

func (c *Computer) assignWaitingRoom() {
	if c.State == StateRemoved {
		return
	}
	for len(c.WaitingLine) > 0 {
		head := c.WaitingLine[0]
		if !c.Occupied.add(head.Demand).within(c.Capacity) {
			return
		}
		c.Occupied = c.Occupied.add(head.Demand)
		c.WaitingLine = c.WaitingLine[1:]
		c.record(DeskAssigned{ComputerID: c.ID, DeskID: head.DeskID})
	}
}

func (c *Computer) record(event Event) {
	if c.Events == nil {
		c.Events = NewEventCollection()
	}
	c.Events.Add(event)
}
