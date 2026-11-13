package domain

import (
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

var (
	// ErrStandaloneWorkFacts refuses a Work Key or a File Home on a Standalone
	// Work (Standalone Work facts).
	ErrStandaloneWorkFacts = errors.New("agent: a Standalone Work has no Work Key and no File Home")
	// ErrWorkCallerNotOwner refuses a Standalone Work whose Work Caller is
	// not its owner (Work Caller record).
	ErrWorkCallerNotOwner = errors.New("agent: a Standalone Work's Work Caller is its owner")
	// ErrMismatchedReuse refuses a creation whose Work Key names a Work of
	// another Work Caller or File Home (Mismatched reuse).
	ErrMismatchedReuse = errors.New("agent: the reused Work has another Work Caller, Work Key or File Home")
)

// WorkCreationFacts are the Works a creation supplies, loaded through the
// AgentWork repository: the definition's Work of the requested Work Key, a
// closed one included, and the source Work of a requested starting context;
// nil for none.
type WorkCreationFacts struct {
	KeyWork    *AgentWork
	SourceWork *AgentWork
}

type WorkCreationRequest struct {
	// StartedBy is the participant starting the Work: one of the definition's
	// Owners, or an extension acting on their behalf, which names that Owner.
	StartedBy          string
	RepositoryInput    WorkRepositoryInput
	AuthorizationInput WorkAuthorizationInput
	Admission          StartAdmission
	ThinkingLevel      string
	WorkModel          WorkModelSelection
	// ComputerManaged reports that the Work starts on a Managed Computer.
	ComputerManaged bool
	// HarnessChoice is how its starter runs the Harness, its permission mode
	// and sandbox, admitted only on a Managed Computer; it does not select
	// which Agent Harness runs, which AgentHarnessID names.
	HarnessChoice HarnessChoice
	// StartingUser is the person starting the Work; empty for an automated
	// start.
	StartingUser string
	// AgentHarnessID names which Agent Harness, the native agent program,
	// the new Work runs; empty chooses the default (Harness choice).
	AgentHarnessID string
	// WorkCaller is in whose name the Work is started; WorkKey and FileHome
	// are the extension's optional facts (Supplied facts).
	WorkCaller      WorkCaller
	WorkKey         WorkKey
	FileHome        FileHome
	Capabilities    CapabilitySnapshot
	TraceAttributes map[string]string
	// WorkLabels are the starter's Work Labels for a new Work; a reused Work
	// keeps its own.
	WorkLabels WorkLabels
	// WorkID, when supplied, is the identity application coordination
	// allocated beforehand, such as the owner of a Standalone Work's File
	// Space; otherwise the Work gets a new one.
	WorkID string
	// DisplayTitle is a Standalone Work's initial display title.
	DisplayTitle string
	// StartingContext, when supplied, starts a new Work from another Work's
	// context; reuse neither compares nor replaces it.
	StartingContext *StartingContextSource
	// InitialTaskInput, when supplied, is the new Work's first task input;
	// a reused Work keeps its own.
	InitialTaskInput *InitialTaskInput
}

// AdmitExtensionWorkCaller refuses a trusted service's creation of, or
// selection by, a Work Caller of a kind Agent defines: such a Work is a
// Standalone Work, which only its owner starts and reads, so an extension
// starts and selects Works only under a kind it defines (Extension Work
// facts).
func AdmitExtensionWorkCaller(caller WorkCaller) error {
	if err := caller.Validate(); err != nil {
		return err
	}
	if !caller.ExtensionDefined() {
		return ErrStandaloneWorkOwnerOnly
	}
	return nil
}

// OwnerWorkCaller is the Work Caller of this definition's Standalone Works,
// its owner: the Application that owns an Application's Agent, or the person
// a person's Agent represents. Any other definition starts no Standalone
// Work.
func (a *AgentDefinition) OwnerWorkCaller() (WorkCaller, bool) {
	if application := a.OwningApplication(); application != "" {
		return WorkCaller{Kind: CallerKindApplication, ID: application}, true
	}
	if person := a.RepresentedPerson(); person != "" {
		return WorkCaller{Kind: CallerKindPerson, ID: person}, true
	}
	return WorkCaller{}, false
}

// OwnerWorkCaller is the Work Caller of the Standalone Works of the
// definition this input locates or establishes, as that definition names it.
func (i AgentDefinitionInput) OwnerWorkCaller() (WorkCaller, bool) {
	definition := AgentDefinition{UsageScope: i.UsageScope, Purpose: i.Purpose}
	return definition.OwnerWorkCaller()
}

// admitWorkFacts admits the facts a new Work fixes at creation: a valid Work
// Caller, and an optional Work Key and File Home. A Standalone Work records
// its owner as its Work Caller and has neither of the others.
func (a *AgentDefinition) admitWorkFacts(caller WorkCaller, key WorkKey, home FileHome) error {
	if err := caller.Validate(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if err := home.Validate(); err != nil {
		return err
	}
	if caller.ExtensionDefined() {
		return nil
	}
	if key.Present() || home.Present() {
		return ErrStandaloneWorkFacts
	}
	if owner, ok := a.OwnerWorkCaller(); !ok || owner != caller {
		return ErrWorkCallerNotOwner
	}
	return nil
}

// ReuseWork locates this definition's Work for the Work Key before new-work
// facts are gathered: Prompt, Plugins and their configuration never decide
// reuse, and a closed Work keeps its Work Key. A Work Key's Work of another
// Work Caller or File Home is refused. A creation without a Work Key reuses
// nothing; only its creation request identity deduplicates it.
func (a *AgentDefinition) ReuseWork(caller WorkCaller, key WorkKey, home FileHome, keyWork *AgentWork) (*AgentWork, error) {
	if err := a.admitWorkFacts(caller, key, home); err != nil {
		return nil, err
	}
	if !key.Present() || keyWork == nil || keyWork.AgentID != a.ID || keyWork.WorkKey != key {
		return nil, nil
	}
	if err := keyWork.AdmitReuse(caller, key, home); err != nil {
		return nil, err
	}
	return keyWork, nil
}

// AdmitReuse refuses answering a creation with this Work when the creation
// supplies another Work Caller, Work Key or File Home than the Work fixed,
// whether its Work Key or its repeated request identity found it (Mismatched
// reuse).
func (s *AgentWork) AdmitReuse(caller WorkCaller, key WorkKey, home FileHome) error {
	if s.WorkCaller != caller || s.WorkKey != key || s.FileHome != home {
		return ErrMismatchedReuse
	}
	return nil
}

// CreateWork returns the retained or newly created Work; created is true only for a new Work.
func (a *AgentDefinition) CreateWork(request WorkCreationRequest, facts WorkCreationFacts) (work *AgentWork, created bool, err error) {
	existing, err := a.ReuseWork(request.WorkCaller, request.WorkKey, request.FileHome, facts.KeyWork)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	if err := request.Capabilities.Validate(); err != nil {
		return nil, false, err
	}
	if len(request.Capabilities.ApplicationEndpoints) > 0 && a.OwningApplication() == "" {
		return nil, false, fmt.Errorf("%w: only an Application's Work takes Application MCP endpoints", ErrInvalidCapabilities)
	}
	for _, slug := range a.Capabilities.RequiredPlugins {
		if !slices.ContainsFunc(request.Capabilities.Plugins, func(p SelectedPlugin) bool { return p.Release.Slug == slug }) {
			return nil, false, fmt.Errorf("%w: missing required plugin %s", ErrInvalidCapabilities, slug)
		}
	}
	if owner := request.AuthorizationInput.OwnerID; owner != "" && owner != a.AuthorizationOwner() {
		return nil, false, fmt.Errorf("%w: Provider Authorizations require a represented human or an owning Application", ErrInvalidWork)
	}
	if owner := request.RepositoryInput.OwnerID; owner != "" && owner != a.AuthorizationOwner() {
		return nil, false, fmt.Errorf("%w: repositories require a represented human or an owning Application", ErrInvalidWork)
	}
	if err := AdmitAgentOwner(a.AdmittedOwners(), request.StartedBy); err != nil {
		return nil, false, err
	}
	if err := a.AdmitComputer(request.ComputerManaged); err != nil {
		return nil, false, err
	}
	starting, err := a.startingContext(request.StartingContext, request.WorkCaller, facts.SourceWork)
	if err != nil {
		return nil, false, err
	}
	workID := request.WorkID
	if workID == "" {
		workID = uuid.Must(uuid.NewV7()).String()
	}
	session, err := NewAgentWork(WorkInput{
		StartedBy:          request.StartedBy,
		RepositoryInput:    request.RepositoryInput,
		AuthorizationInput: request.AuthorizationInput,
		ThinkingLevel:      request.ThinkingLevel,
		ID:                 workID, AgentID: a.ID, WorkCaller: request.WorkCaller, WorkKey: request.WorkKey, FileHome: request.FileHome, DisplayTitle: request.DisplayTitle,
		WorkModel: request.WorkModel, Capabilities: request.Capabilities, RepresentedPerson: a.RepresentedPerson(), OwningApplication: a.OwningApplication(), StartingUser: request.StartingUser,
		WorkLayout: a.WorkLayout, AgentHarnessID: request.AgentHarnessID, TraceAttributes: request.TraceAttributes, WorkLabels: request.WorkLabels,
		Visibility: a.Visibility, StartingContext: starting, ComputerManaged: request.ComputerManaged, HarnessChoice: request.HarnessChoice, InitialTaskInput: request.InitialTaskInput,
	}, request.Admission)
	if err != nil {
		return nil, false, err
	}
	session.DefinitionVersion = a.Version
	return session, true, nil
}
