package domain

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

var (
	ErrInvalidAgentDefinition = errors.New("agent: invalid definition")
	ErrInvalidCapabilities    = errors.New("agent: invalid capabilities")
	validation                = validator.New()
)

// CapabilityConfiguration declares requirements for new Works, not personal defaults.
type CapabilityConfiguration struct {
	Prompt          string
	StartupAgent    string
	RequiredPlugins []string
}

type CapabilityDifferences struct {
	Prompt       *string
	StartupAgent *string
	Plugins      []PluginChoice
	Connectors   []ConnectorChoice
	// RequiredPlugins are the caller's own, such as the plugin of the role it
	// names; a per-Work exclusion does not drop them.
	RequiredPlugins []string
}

// WithRequiredPlugins is a definition's required plugins and those a caller's
// Work adds, each once, the definition's first.
func WithRequiredPlugins(definition, caller []string) []string {
	result := slices.Clone(definition)
	for _, slug := range caller {
		if !slices.Contains(result, slug) {
			result = append(result, slug)
		}
	}
	return result
}

func (c CapabilityConfiguration) Validate() error {
	seen := map[string]bool{}
	for _, slug := range c.RequiredPlugins {
		if !pluginconfig.ValidPluginName(slug) || seen[slug] {
			return ErrInvalidCapabilities
		}
		seen[slug] = true
	}
	return nil
}

func (c CapabilityConfiguration) Clone() CapabilityConfiguration {
	c.RequiredPlugins = slices.Clone(c.RequiredPlugins)
	return c
}

type UsageScope struct {
	Kind      string `validate:"required,max=64"`
	Reference string `validate:"required,max=255"`
}

func (s UsageScope) Validate() error {
	if err := validation.Struct(s); err != nil || strings.TrimSpace(s.Kind) == "" || strings.TrimSpace(s.Reference) == "" {
		return ErrInvalidAgentDefinition
	}
	return nil
}

type AgentDefinitionInput struct {
	// Owners are the people the Agent acts for: the person for a person's
	// Agent, the Project's Owner for a project-scoped definition's Agent.
	Owners       []string
	AgentName    string
	Purpose      string
	UsageScope   UsageScope
	HomeBinding  HomeBinding
	Visibility   VisibilityRule
	Capabilities CapabilityConfiguration
}

// WorkCreationFacts are the Works a creation supplies, loaded through the
// AgentWork repository: the definition's Work of the requested Subject, a
// closed one included, and the source Work of a requested starting context;
// nil for none.
type WorkCreationFacts struct {
	SubjectWork *AgentWork
	SourceWork  *AgentWork
}

type WorkCreationRequest struct {
	// StartedBy is the participant starting the Work: one of the definition's
	// Owners, or a caller acting on their behalf, who names that Owner.
	StartedBy          string
	RepositoryInput    WorkRepositoryInput
	AuthorizationInput WorkAuthorizationInput
	Admission          StartAdmission
	ThinkingLevel      string
	WorkModel          WorkModelSelection
	// ComputerManaged reports that the Work starts on a Managed Computer.
	ComputerManaged bool
	// Harness is its starter's choice, admitted only on a Managed Computer.
	Harness            HarnessChoice
	Subject            WorkSubject
	ProjectID          string
	WorkspaceReference string
	InputSource        WorkInputSource
	ReadOnlyMounts     []ReadOnlyMount
	Capabilities       CapabilitySnapshot
	TraceAttributes    map[string]string
	// WorkID, when supplied, is the identity application coordination
	// allocated beforehand, such as the owner of a Personal Work's File
	// Space; otherwise the Work gets a new one.
	WorkID string
	// DisplayTitle is a Personal Work's initial display title.
	DisplayTitle string
	// StartingContext, when supplied, starts a new Work from another Work's
	// context; reuse neither compares nor replaces it.
	StartingContext *StartingContextSource
	// InitialTaskInput, when supplied, is the new Work's first task input;
	// a reused Work keeps its own.
	InitialTaskInput *InitialTaskInput
}

// RepresentsHuman reports whether this definition's Works belong to a human:
// an employee scope. A project scope's Works belong to the definition's own
// Agent, which represents no human.
func (a *AgentDefinition) RepresentsHuman() bool { return a.UsageScope.Kind == "employee" }

// WorkPrincipal is whom every Work of this definition acts as: a person's
// Agent acts for that person, its first Owner; an Agent that represents no
// human, such as a project-scoped one, acts as the definition itself, never as
// the human who owns it. The definition is never one of its own Owners.
func (a *AgentDefinition) WorkPrincipal() string {
	if !a.RepresentsHuman() {
		return a.ID
	}
	if len(a.Owners) == 0 {
		return ""
	}
	return a.Owners[0]
}

// PersonalAuthorizationOwner identifies the represented human, never a caller
// or a project-owned automated Agent's triggering user.
func (a *AgentDefinition) PersonalAuthorizationOwner() string {
	if a.Purpose == "work" && a.UsageScope.Kind == "employee" && a.UsageScope.Reference == a.HomeBinding.OwnerID {
		return a.UsageScope.Reference
	}
	return ""
}

// WorkTraceAttributes is what a new Work's telemetry carries from its
// creation: the attributes its caller supplied, beside the definition's own
// facts no caller attribute replaces, its runtime purpose and the person its
// Agent represents, the one Observability rolls the Work's usage up to. A
// definition that represents no person gives none.
func (a *AgentDefinition) WorkTraceAttributes(supplied map[string]string) map[string]string {
	attributes := maps.Clone(supplied)
	if attributes == nil {
		attributes = make(map[string]string)
	}
	attributes["talgent.runtime_purpose"] = a.Purpose
	delete(attributes, "talgent.user_id")
	if person := a.PersonalAuthorizationOwner(); person != "" {
		attributes["talgent.user_id"] = person
	}
	return attributes
}

type AgentDefinition struct {
	// Owners are the Agent Owners of every Work of this definition, a set
	// (one person today); no Work records its own.
	Owners       []string
	AgentName    string `validate:"max=512"`
	Icon         AgentIcon
	Purpose      string `validate:"required,max=64"`
	UsageScope   UsageScope
	HomeBinding  HomeBinding
	Visibility   VisibilityRule
	ID           string `validate:"required,uuid"`
	Capabilities CapabilityConfiguration
	// Soul is the persona and working style its Owners write; each of its
	// Works takes it up at its next start. Only its Owners read or write it.
	Soul    string
	Version int `validate:"gte=0"`
}

func NewAgentDefinition(input AgentDefinitionInput) (*AgentDefinition, error) {
	agent := &AgentDefinition{Owners: normalizeOwners(input.Owners), AgentName: strings.TrimSpace(input.AgentName), Icon: DefaultAgentIcon, Purpose: strings.TrimSpace(input.Purpose), ID: uuid.Must(uuid.NewV7()).String(), UsageScope: input.UsageScope, HomeBinding: input.HomeBinding, Visibility: input.Visibility, Capabilities: input.Capabilities.Clone()}
	if err := agent.Validate(); err != nil {
		return nil, err
	}
	return agent, nil
}

func (a *AgentDefinition) Validate() error {
	if err := a.UsageScope.Validate(); err != nil {
		return err
	}
	if err := a.HomeBinding.Validate(); err != nil {
		return err
	}
	if err := validation.Struct(a); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAgentDefinition, err)
	}
	if err := a.Icon.Validate(); err != nil {
		return err
	}
	if err := validateOwners(a.Owners, a.ID); err != nil {
		return err
	}
	if err := validateSoul(a.Soul); err != nil {
		return err
	}
	return a.Capabilities.Validate()
}

// IsOwner reports whether the participant is one of the Agent's Owners.
func (a *AgentDefinition) IsOwner(participant string) bool {
	return AdmitAgentOwner(a.Owners, participant) == nil
}

// TransferOwners replaces the Owners with the supplied people; every Work of
// the definition answers to them from then on. It reports whether they
// changed.
func (a *AgentDefinition) TransferOwners(owners []string) (bool, error) {
	owners = normalizeOwners(owners)
	if err := validateOwners(owners, a.ID); err != nil {
		return false, err
	}
	if slices.Equal(owners, a.Owners) {
		return false, nil
	}
	a.Owners = owners
	return true, nil
}

// AdmitAgentOwner is the Agent Owner rule: only one of the Agent's Owners, or
// a caller acting on their behalf who names that Owner, gives its Works
// input, answers their Required Responses and controls them.
func AdmitAgentOwner(owners []string, participant string) error {
	if participant == "" || !slices.Contains(owners, participant) {
		return ErrControlRequesterNotOwner
	}
	return nil
}

func normalizeOwners(owners []string) []string {
	out := make([]string, 0, len(owners))
	for _, owner := range owners {
		if owner = strings.TrimSpace(owner); owner != "" && !slices.Contains(out, owner) {
			out = append(out, owner)
		}
	}
	slices.Sort(out)
	return out
}

// validateOwners requires at least one Owner; an AgentDefinition is never
// its own Owner.
func validateOwners(owners []string, definitionID string) error {
	if len(owners) == 0 {
		return fmt.Errorf("%w: an Agent has at least one Owner", ErrInvalidAgentDefinition)
	}
	for _, owner := range owners {
		if len(owner) > 255 || owner == definitionID {
			return fmt.Errorf("%w: invalid Owner", ErrInvalidAgentDefinition)
		}
	}
	return nil
}

func (a *AgentDefinition) ChangeCapabilities(capabilities CapabilityConfiguration) error {
	if err := capabilities.Validate(); err != nil {
		return err
	}
	a.Capabilities = capabilities.Clone()
	return nil
}

func (a *AgentDefinition) ProvideCapabilities() CapabilityConfiguration {
	return a.Capabilities.Clone()
}

// ReuseWork locates this definition's Work for the Subject before new-work
// facts are gathered: Prompt, Plugins and their configuration never decide
// reuse, and a closed Work keeps its Subject. A Work without a Subject is
// never reused; only its creation request identity deduplicates it.
func (a *AgentDefinition) ReuseWork(subject WorkSubject, subjectWork *AgentWork) (*AgentWork, error) {
	if err := subject.Validate(); err != nil {
		return nil, err
	}
	if !subject.Present() || subjectWork == nil || subjectWork.AgentID != a.ID || subjectWork.Subject != subject {
		return nil, nil
	}
	return subjectWork, nil
}

// CreateWork returns the retained or newly created Work; created is true only for a new Work.
func (a *AgentDefinition) CreateWork(request WorkCreationRequest, facts WorkCreationFacts) (work *AgentWork, created bool, err error) {
	existing, err := a.ReuseWork(request.Subject, facts.SubjectWork)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	if err := request.Capabilities.Validate(); err != nil {
		return nil, false, err
	}
	for _, slug := range a.Capabilities.RequiredPlugins {
		if !slices.ContainsFunc(request.Capabilities.Plugins, func(p SelectedPlugin) bool { return p.Release.Slug == slug }) {
			return nil, false, fmt.Errorf("%w: missing required plugin %s", ErrInvalidCapabilities, slug)
		}
	}
	if owner := request.AuthorizationInput.OwnerUserID; owner != "" && (a.Purpose != "work" || a.UsageScope.Kind != "employee" || a.UsageScope.Reference != owner) {
		return nil, false, fmt.Errorf("%w: personal authorization requires a represented human", ErrInvalidWork)
	}
	if owner := request.RepositoryInput.OwnerUserID; owner != "" && owner != a.PersonalAuthorizationOwner() {
		return nil, false, fmt.Errorf("%w: personal repositories require a represented human", ErrInvalidWork)
	}
	if err := AdmitAgentOwner(a.Owners, request.StartedBy); err != nil {
		return nil, false, err
	}
	if err := a.AdmitComputer(request.ComputerManaged); err != nil {
		return nil, false, err
	}
	starting, err := a.startingContext(request.StartingContext, request.ProjectID, facts.SourceWork)
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
		ID:                 workID, AgentID: a.ID, Subject: request.Subject, ProjectID: request.ProjectID, DisplayTitle: request.DisplayTitle,
		WorkModel: request.WorkModel, Capabilities: request.Capabilities, HomeBinding: a.HomeBinding, WorkspaceReference: request.WorkspaceReference, InputSource: request.InputSource, ReadOnlyMounts: request.ReadOnlyMounts, TraceAttributes: request.TraceAttributes,
		Visibility: a.Visibility, StartingContext: starting, ComputerManaged: request.ComputerManaged, Harness: request.Harness, InitialTaskInput: request.InitialTaskInput,
	}, request.Admission)
	if err != nil {
		return nil, false, err
	}
	return session, true, nil
}
