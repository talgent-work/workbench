package domain

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrExecutionRequest            = errors.New("agent: execution request failed")
	ErrInvalidWork                 = errors.New("agent: invalid work")
	ErrControlRequesterNotOwner    = errors.New("agent: requester is not the Work owner")
	ErrWorkTransition              = errors.New("agent: work transition not allowed")
	ErrCapacityUnavailable         = errors.New("agent: execution capacity unavailable")
	ErrExecutionNotPermitted       = errors.New("agent: execution permission missing")
	ErrComputeReclamationPending   = errors.New("agent: previous execution reclamation pending")
	ErrModelConfigurationWithdrawn = errors.New("agent: the Work's model configuration is withdrawn")
	// ErrTurnActive rejects stopping a Work while a Turn runs: a Work stops
	// only between Turns, after its Turn ends or is cancelled.
	ErrTurnActive = errors.New("agent: a Turn is running; cancel it before stopping the Work")
	// ErrInputPending rejects closing a running Work that still holds
	// accepted input; it is a work transition refusal.
	ErrInputPending = fmt.Errorf("%w: the Work still has accepted input to finish", ErrWorkTransition)
)

type WorkState string

const (
	WorkStarting WorkState = "starting"
	WorkWorking  WorkState = "working"
	WorkStopping WorkState = "stopping"
	WorkStopped  WorkState = "stopped"
	WorkFailed   WorkState = "failed"
	WorkClosing  WorkState = "closing"
	WorkClosed   WorkState = "closed"
)

// WorkStates are every Work State, in lifecycle order.
var WorkStates = []WorkState{WorkStarting, WorkWorking, WorkStopping, WorkStopped, WorkFailed, WorkClosing, WorkClosed}

type ContextMode string

const (
	ContextFresh    ContextMode = "fresh"
	ContextContinue ContextMode = "continue"
	ContextRecover  ContextMode = "recover"
)

type WorkInput struct {
	StartedBy          string
	RepositoryInput    WorkRepositoryInput
	AuthorizationInput WorkAuthorizationInput
	ThinkingLevel      string
	WorkModel          WorkModelSelection
	Subject            WorkSubject
	ProjectID          string
	ID                 string
	AgentID            string
	Capabilities       CapabilitySnapshot
	// RepresentedPerson is the person the definition's Agent represents;
	// empty for one that represents no person.
	RepresentedPerson string
	// OwningApplication is the Application that owns the definition's Agent;
	// empty for none.
	OwningApplication string
	// StartingUser is the person starting the Work; empty for an automated
	// start.
	StartingUser string
	// WorkLayout is the definition's, which the Work locates once.
	WorkLayout WorkLayout
	// AgentHarnessID names which Agent Harness, the native agent program,
	// the Work runs for its life; empty chooses the default.
	AgentHarnessID  string
	TraceAttributes map[string]string
	// WorkLabels are the starter's Work Labels, kept for the Work's life.
	WorkLabels      WorkLabels
	Visibility      VisibilityRule
	DisplayTitle    string
	StartingContext *StartingContext
	// ComputerManaged reports that the Computer the Work starts on is a
	// Managed Computer; it decides which model selections the Work may keep
	// and that it mounts no Directory.
	ComputerManaged bool
	// HarnessChoice is how its starter runs the Harness, its permission mode
	// and sandbox, admitted only on a Managed Computer; it does not select
	// which Agent Harness runs, which AgentHarnessID names.
	HarnessChoice HarnessChoice
	// InitialTaskInput is the creating caller's first task input, when it
	// supplied one.
	InitialTaskInput *InitialTaskInput
}

// StartAdmission carries the AgentDefinition, Computer and Model
// Configuration facts supplied for one Start, Resume or Restart. AdmittedAt is
// when that start enters Starting, the origin of its Startup Deadline.
type StartAdmission struct {
	AdmittedAt         time.Time
	AgentID            string
	DefinitionAdmitted bool
	// Seat is the Work's Computer's answer admitting this run: seated, or
	// waiting in its line.
	Seat ComputerSeat
	// ModelConfigurationProvided reports that the Work's selected Model
	// Configuration is currently provided; a withdrawn one admits no start.
	// A Work on the machine's Claude login has none and ignores it.
	ModelConfigurationProvided bool
	// Soul is the definition's current Soul, which this start takes up.
	Soul string
	// CurrentPlugins are the current releases of the Work's selected Plugins;
	// a Resume or Restart upgrades to them. A first start already has them.
	CurrentPlugins []PluginRelease
}

type StopReason string

const (
	StopReasonUserStopped    StopReason = "user_stopped"
	StopReasonWaitingTimeout StopReason = "waiting_timeout"
	StopReasonCompleted      StopReason = "completed"
	// StopReasonDisconnected stops a Work whose Disconnection outlasted the
	// Disconnection timeout (Stop beyond reach).
	StopReasonDisconnected StopReason = "disconnected"
)

// HarnessProcess holds the facts of the Work's current Harness process run.
// It has no identity: every run continues the same Harness Session, and the
// facts are reset when a new start is admitted. Phase and Turn sequences are
// positions in the current process's feedback stream.
type HarnessProcess struct {
	PhaseSequence        int64
	WorkProgressSequence int64
	SubAgentsSequence    int64
	// PermissionModeSequence is the position of the latest permission mode
	// the process reported.
	PermissionModeSequence int64
	// SubAgents are those its main Agent delegated to; they end with it.
	SubAgents []SubAgent
	// IdleSince is the origin of the process's idle interval: no active Turn
	// and nothing accepted awaiting processing. WaitingSince is the origin of
	// its Turn's Waiting interval, held by the user's answer.
	IdleSince     time.Time
	WaitingSince  time.Time
	StartingSince time.Time
	ContextMode   ContextMode
	// DeskID is the Desk the admitted start took on the Work's Computer, and
	// DeskWaiting whether its Computer admitted it waiting in its line: the
	// admission its start request carries. The start's AgentExecution holds
	// that Desk and whether it still waits.
	DeskID      string
	DeskWaiting bool
	// StartWithdrawn records that a stop or close withdrew the start while
	// its Desk still waited: it launched nothing and holds no compute.
	StartWithdrawn bool
	// Soul is the definition's Soul this start took up.
	Soul string
	// Ready records its AgentExecution's report that the run became ready.
	Ready            bool
	StartRejected    bool
	Exited           bool
	ComputeReclaimed bool
	EnvironmentLost  bool
	// StartAbandoned records its AgentExecution's report that this start
	// never launched a Harness process and no longer can: its environment is
	// released without a stop or an exit.
	StartAbandoned bool
	// Unreached records that a stop or close during a Disconnection on a
	// Cloud reclaims the run's sandbox in place of its exit; the release
	// confirmation vouches for the exit (Disconnection release).
	Unreached bool
	// Fenced records that its Managed Computer fenced the run during a
	// Disconnection, which vouches for its exit and release.
	Fenced bool
	Turn   TurnObservation
}

// Held reports whether the process may still be running: from start admission
// until exit, permanent environment loss, rejection before any external
// compute creation was submitted, or abandonment of a start that launched no
// Harness process.
func (p *HarnessProcess) Held() bool {
	return p != nil && !p.Exited && !p.EnvironmentLost && !p.StartRejected && !p.StartAbandoned && !p.StartWithdrawn
}

// Equal reports whether two observations of the process hold the same facts.
func (p HarnessProcess) Equal(other HarnessProcess) bool {
	return reflect.DeepEqual(p, other)
}

// rest ends the process's idle interval, as a stop, an exit or a failure
// does; an unanswered question stays Waiting.
func (p *HarnessProcess) rest() {
	p.IdleSince = time.Time{}
}

// ended reports a start with no Harness process left to end: it exited, was
// rejected before compute was submitted, was abandoned without launching one
// past its Startup Deadline, or was withdrawn while its Desk waited.
func (p *HarnessProcess) ended() bool {
	return p.Exited || p.StartRejected || p.StartAbandoned || p.StartWithdrawn
}

// TurnObservation is ordered evidence within the current process, not a child
// Entity. Completed distinguishes a normal result from interruption or failure.
type TurnObservation struct {
	Sequence  int64
	Active    bool
	Completed bool
}

type AgentWork struct {
	// StartedBy is who started the Work, fixed at creation: an Owner of its
	// definition when it started, kept as a fact, never as authority. The
	// Work's Agent Owners are its definition's Owners.
	StartedBy           string `validate:"max=255"`
	RepositorySelection WorkRepositorySelection
	// RepositoryCheckouts are the final checkout outcomes of selected
	// repositories, in selection order; a repository without one is still to
	// be checked out.
	RepositoryCheckouts    []RepositoryCheckout
	AuthorizationSelection WorkAuthorizationSelection
	// InitialTaskInput is the creating caller's first task input, held until
	// it is handed to the Work's input buffer; nil once handed off, or for a
	// Work created without one.
	InitialTaskInput *InitialTaskInput
	ThinkingLevel    string
	// PermissionMode is a Managed Work's Harness permission mode, the one its
	// Harness last reported; empty for a Cloud Work, which always bypasses.
	PermissionMode PermissionMode
	Progress       *WorkProgress
	WorkModel      WorkModelSelection
	// Subject is the optional business object this Work is about; ProjectID
	// is the Work's optional Project, held beside it for visibility and
	// authorization.
	Subject            WorkSubject
	ProjectID          string    `validate:"max=255"`
	ID                 string    `validate:"required,uuid"`
	AgentID            string    `validate:"required,uuid"`
	State              WorkState `validate:"oneof=starting working stopping stopped failed closing closed"`
	CapabilitySnapshot CapabilitySnapshot
	// ComputerID and ComputerKind are the Work's ComputerReference: the
	// Computer chosen at its first start and whether it is a Cloud or a
	// Managed Computer, as that Computer's admission answered, fixed for its
	// life; every run of the Work is admitted there.
	ComputerID   string       `validate:"required,uuid"`
	ComputerKind ComputerKind `validate:"oneof=cloud managed"`
	// HarnessSessionReference is the Work's one Harness Session within its
	// Harness state Directory.
	HarnessSessionReference string `validate:"required"`
	// AgentHarnessReference is the Agent Harness chosen at creation, kept for
	// the Work's life (Harness choice).
	AgentHarnessReference string `validate:"required"`
	// LocatedLayout is what the definition's Work Layout located from the
	// Work's facts at creation, kept whatever the layout becomes later.
	LocatedLayout LocatedLayout
	// RepresentedPerson is the person the Work's Agent represents, fixed at
	// creation; empty for a Work whose Agent represents no person.
	RepresentedPerson string `validate:"max=255"`
	// OwningApplication is the Application that owns the Work, fixed at
	// creation; empty for a Work no Application owns.
	OwningApplication string `validate:"max=255"`
	// StartingContext is the context the Work started from, fixed at
	// creation; nil for a Work that started afresh.
	StartingContext *StartingContext
	// DisplayTitle is a Standalone Work's title, changed by its owner at any
	// time; it never identifies the Work.
	DisplayTitle    string
	TraceAttributes map[string]string
	// WorkLabels are the Work Labels its starter gave it at creation, kept
	// for good (Labels fixed); they never grant or limit access to it
	// (Labels grant nothing).
	WorkLabels     WorkLabels
	HarnessProcess *HarnessProcess
	// DisconnectedSince is the origin of the current Disconnection: the
	// platform lost observation of the Work's execution environment then. It
	// changes no State and is zero while the environment is observed.
	DisconnectedSince time.Time
	// ReachResumeDue marks input awaiting a Managed Computer that is away: a
	// Resume it refused as unreachable, or a stop beyond reach with input
	// awaiting. The Resume is requested once more when the machine
	// reconnects, if input awaits then (reach_resume.go).
	ReachResumeDue bool
	// ReachResumeFailures counts the retries of the owed Resume that failed
	// without an answer; the one reaching ReachResumeAttempts spends the mark.
	ReachResumeFailures   int
	StopReason            StopReason `validate:"omitempty,oneof=user_stopped waiting_timeout completed disconnected"`
	StopRequestReference  string
	CloseRequestReference string
	EndRequestReference   string
	FailureReason         string
	// Visibility decides who reads the message stream; WorkShares are the
	// readers the owner lets read it while private.
	Visibility WorkVisibility
	WorkShares []string
	Events     EventCollection
	Version    int `validate:"gte=0"`
	// DefinitionVersion is the AgentDefinition version a new Work was created
	// under; its first save commits only while the definition is unchanged
	// (Static creation admission). A loaded Work does not carry it.
	DefinitionVersion int
}

func NewAgentWork(input WorkInput, admission StartAdmission) (*AgentWork, error) {
	if strings.TrimSpace(input.StartedBy) == "" {
		return nil, fmt.Errorf("%w: who starts the Work is required", ErrInvalidWork)
	}
	harness, err := input.HarnessChoice.On(input.ComputerManaged)
	if err != nil {
		return nil, err
	}
	capabilities := input.Capabilities.Clone().AvailableOn(input.ComputerManaged)
	capabilities.Sandbox = harness.Sandbox
	session := &AgentWork{
		StartedBy: input.StartedBy, PermissionMode: harness.PermissionMode,
		WorkModel: input.WorkModel, ID: input.ID, AgentID: input.AgentID, ThinkingLevel: input.ThinkingLevel, State: WorkStarting, ComputerID: admission.Seat.ComputerID, ComputerKind: admission.Seat.Kind, HarnessProcess: admission.process(ContextFresh, time.Time{}), Subject: input.Subject, ProjectID: strings.TrimSpace(input.ProjectID),
		CapabilitySnapshot: capabilities, HarnessSessionReference: uuid.Must(uuid.NewV7()).String(),
		RepresentedPerson: input.RepresentedPerson, OwningApplication: input.OwningApplication, DisplayTitle: strings.TrimSpace(input.DisplayTitle), TraceAttributes: maps.Clone(input.TraceAttributes), WorkLabels: input.WorkLabels,
		Visibility: WorkVisibility{Public: input.Visibility.Public && strings.TrimSpace(input.ProjectID) != ""}, StartingContext: input.StartingContext.clone(), Events: NewEventCollection(),
	}
	if initial := input.InitialTaskInput; initial != nil {
		established, err := NewInitialTaskInput(initial.ID, initial.Text)
		if err != nil {
			return nil, err
		}
		session.InitialTaskInput = &established
	}
	if err := session.locateLayout(input.WorkLayout, input.AgentHarnessID, input.StartingUser, input.ComputerManaged); err != nil {
		return nil, err
	}
	if err := session.determineAuthorizationSelection(input.AuthorizationInput); err != nil {
		return nil, err
	}
	if err := session.determineRepositorySelection(input.RepositoryInput); err != nil {
		return nil, err
	}
	if err := validateStartAdmission(session.AgentID, session.WorkModel, admission); err != nil {
		return nil, err
	}
	// The Computer's admission names what it is; a Computer that answers
	// otherwise than the starter chose it is not the one chosen.
	if admission.Seat.Kind != kindOf(input.ComputerManaged) {
		return nil, fmt.Errorf("%w: the admitting Computer is not of the kind chosen", ErrComputerNotUsable)
	}
	if err := session.WorkModel.AdmitOn(input.ComputerManaged); err != nil {
		return nil, err
	}
	if err := session.Validate(); err != nil {
		return nil, err
	}
	session.admitStart()
	return session, nil
}

// AdmitControlRequester admits input, interaction responses, Resume, Restart,
// stop and closure requests only from one of its definition's Owners, or a
// caller acting on their behalf who names that Owner.
func (s *AgentWork) AdmitControlRequester(definition *AgentDefinition, participant string) error {
	if definition == nil || definition.ID != s.AgentID {
		return ErrControlRequesterNotOwner
	}
	return AdmitAgentOwner(definition.Owners, participant)
}

func (s *AgentWork) Validate() error {
	if s.Progress != nil {
		if err := s.Progress.Validate(); err != nil {
			return err
		}
	}
	if err := s.WorkModel.Validate(); err != nil {
		return err
	}
	if s.ThinkingLevel != "" && !slices.Contains(ThinkingLevels, s.ThinkingLevel) {
		return fmt.Errorf("%w: unknown thinking level", ErrInvalidWork)
	}
	if err := s.Subject.Validate(); err != nil {
		return err
	}
	if err := validation.Struct(s); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidWork, err)
	}
	if err := s.CapabilitySnapshot.Validate(); err != nil {
		return err
	}
	if err := s.validateHarness(); err != nil {
		return err
	}
	if err := s.AuthorizationSelection.Validate(); err != nil {
		return err
	}
	if err := s.RepositorySelection.Validate(); err != nil {
		return err
	}
	if err := validateDisplayTitle(s.DisplayTitle, s.Standalone()); err != nil {
		return err
	}
	if err := s.validateLocatedLayout(); err != nil {
		return err
	}
	if err := s.StartingContext.validate(); err != nil {
		return err
	}
	if err := s.validateVisibility(); err != nil {
		return err
	}
	if s.StartedBy != "" && s.StartedBy == s.AgentID {
		return fmt.Errorf("%w: an AgentDefinition never starts a Work", ErrInvalidWork)
	}
	if s.RepresentedPerson != "" && s.OwningApplication != "" {
		return fmt.Errorf("%w: a Work represents a person or belongs to an Application, never both", ErrInvalidWork)
	}
	if owner := s.RepositorySelection.OwnerUserID; owner != "" && owner != s.AuthorizationOwner() {
		return ErrInvalidWork
	}
	if owner := s.AuthorizationSelection.OwnerUserID; owner != "" && owner != s.AuthorizationOwner() {
		return ErrInvalidWork
	}
	if s.HarnessProcess == nil && s.State != WorkClosed {
		return ErrInvalidWork
	}
	if p := s.HarnessProcess; p != nil {
		// Only a Working Work is idle; a closing or closed one owes no answer.
		if (s.State != WorkWorking && !p.IdleSince.IsZero()) || (s.Closed() && !p.WaitingSince.IsZero()) {
			return ErrInvalidWork
		}
		// Every process belongs to an admitted start, which entered Starting.
		if p.StartingSince.IsZero() {
			return ErrInvalidWork
		}
		if p.ContextMode != ContextFresh && p.ContextMode != ContextContinue && p.ContextMode != ContextRecover {
			return ErrInvalidWork
		}
	}
	return nil
}

// AdmitPreparation admits preparing the Work's execution until it closes.
func (s *AgentWork) AdmitPreparation() error {
	if s.State == WorkClosing || s.State == WorkClosed {
		return ErrWorkTransition
	}
	return nil
}
