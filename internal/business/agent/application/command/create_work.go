package command

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type CreateWork struct {
	RequestID string
	WorkModel domain.WorkModelSelection
	// InitialExecution carries the new Work's initial task input, initial
	// thinking level and the repositories its first start checks out.
	InitialExecution *PrepareWorkExecution
	AgentID          string
	SubjectID        string
	// StartingUserID is the trusted represented human. Empty means an
	// automated start without personal defaults.
	StartingUserID string
	// WorkCaller is in whose name the Work is started: the owner of a
	// Standalone Work, or the extension's object for an Extension Work, which
	// may also supply a Work Key and a File Home.
	WorkCaller            domain.WorkCaller
	WorkKey               domain.WorkKey
	FileHome              domain.FileHome
	TraceAttributes       map[string]string
	CapabilityDifferences domain.CapabilityDifferences
	// StartingContext, when supplied, starts a new Work from another Work of
	// the same definition; a reused Work keeps its own.
	StartingContext *domain.StartingContextSource
	// WorkID, when supplied, is the identity the caller allocated; a Standalone
	// Work's File Space is opened under it before the Work exists. Otherwise
	// the creation request identity names the Work.
	WorkID string
	// DisplayTitle is a Standalone Work's initial display title.
	DisplayTitle string
	// ComputerID is the Computer its starter chose; empty runs it on the
	// default Computer (see workComputer).
	ComputerID string
	// Harness is the starter's choice for the Work's Harness, admitted only
	// on a Managed Computer.
	Harness domain.HarnessChoice
	// AgentHarness names the Agent Harness the Work runs; empty chooses the
	// default.
	AgentHarness string
	// WorkLabels are the starter's Work Labels for a new Work.
	WorkLabels domain.WorkLabels
}

// CreateWorkHandler creates at most one Work per caller request identity: the
// identity derives the new Work's identity and its first Desk, so a repeat
// after a lost answer, or after a commit whose outcome it never learned,
// reaches the same Work and Desk. The caller owns the identity, its admission
// and its recovery; no request record is kept.
type CreateWorkHandler struct {
	now           func() time.Time
	builtIns      domain.BuiltInPlugins
	catalog       MarketplacePort
	computers     ComputerPort
	models        query.ModelConfigurations
	agents        domain.AgentDefinitionRepository
	sessions      domain.AgentWorkRepository
	authorization access.WorkAuthorizer
	prepare       *PrepareWorkExecutionHandler
}

func NewCreateWorkHandler(now func() time.Time, builtIns domain.BuiltInPlugins, agents domain.AgentDefinitionRepository, sessions domain.AgentWorkRepository, authorization access.WorkAuthorizer, models query.ModelConfigurations, catalog MarketplacePort, computers ComputerPort, prepare *PrepareWorkExecutionHandler) *CreateWorkHandler {
	return &CreateWorkHandler{now: now, builtIns: builtIns, catalog: catalog, computers: computers, agents: agents, sessions: sessions, authorization: authorization, models: models, prepare: prepare}
}

// creationAttempts bounds retries after a competing change of the
// definition or of the request's own Work between reading and committing.
const creationAttempts = 8

func (h *CreateWorkHandler) Handle(ctx context.Context, request CreateWork) (string, error) {
	request, err := normalizeCreation(request)
	if err != nil {
		return "", err
	}
	for range creationAttempts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Read on every attempt: a competing creation of the same request
		// identity that committed first is found here.
		existing, err := h.requestedWork(ctx, request)
		if err != nil {
			return "", err
		}
		if existing != nil {
			return h.created(ctx, request, existing.ID)
		}
		workID, err := h.create(ctx, request)
		if errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return workID, err
	}
	return "", domain.ErrConcurrentModification
}

// requestedWork is the Work the request identity names, nil before it exists.
// An identity naming a Work of another definition belongs to another
// operation.
func (h *CreateWorkHandler) requestedWork(ctx context.Context, request CreateWork) (*domain.AgentWork, error) {
	work, err := h.sessions.Get(ctx, request.workID())
	if errors.Is(err, domain.ErrWorkNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if work.AgentID != request.AgentID {
		return nil, ErrStartRequestConflict
	}
	return work, nil
}

// workID is the identity of the Work the request establishes: the one its
// caller allocated, or else the one its request identity derives.
func (request CreateWork) workID() string {
	if request.WorkID != "" {
		return request.WorkID
	}
	return requestedWorkID(request.RequestID)
}

// normalizeCreation validates a creation request and completes its initial
// execution with the creation's correlation.
func normalizeCreation(request CreateWork) (CreateWork, error) {
	if initial := request.InitialExecution; initial != nil {
		if strings.TrimSpace(request.SubjectID) == "" {
			return request, access.ErrAccessDenied
		}
		if initial.WorkID != "" {
			return request, domain.ErrInvalidWork
		}
		if err := validateInitialPreparation(*initial); err != nil {
			return request, err
		}
		if request.RequestID != "" && request.RequestID != initial.RequestID {
			return request, ErrStartRequestConflict
		}
		completed := *initial
		completed.SubjectID = request.SubjectID
		request.RequestID, request.InitialExecution = initial.RequestID, &completed
	}
	if uuid.Validate(request.RequestID) != nil {
		return request, ErrInvalidStartRequest
	}
	return request, nil
}

// created answers the Work this request created or reused: an authorized
// caller receives it, and a request carrying an initial execution receives it
// only when it asks for the repositories the Work retained.
func (h *CreateWorkHandler) created(ctx context.Context, request CreateWork, workID string) (string, error) {
	if request.InitialExecution == nil {
		if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, workID); err != nil {
			return "", err
		}
		return workID, nil
	}
	initial := *request.InitialExecution
	initial.WorkID = workID
	return h.prepare.Handle(ctx, initial)
}

// workModel is the model a new Work retains: the requested one, or, with none
// requested, the machine's Claude login on a Managed Computer, and elsewhere
// the definition's default model while it is provided, else the first
// provided Model Configuration (Model choice).
func (h *CreateWorkHandler) workModel(requested domain.WorkModelSelection, defaults domain.WorkDefaults, managed bool) domain.WorkModelSelection {
	if requested != (domain.WorkModelSelection{}) {
		return requested
	}
	if managed {
		return domain.MachineLoginModel()
	}
	if _, provided := h.models.Option(defaults.Model()); defaults.ModelConfigurationID != "" && provided {
		return defaults.Model()
	}
	if option, ok := h.models.Default(); ok {
		return option.Selection
	}
	return requested
}

// defaultThinkingLevel is what a start that names no thinking level takes:
// the definition's default when the Work's model offers it, and otherwise
// the Model Template default (Thinking-level default).
func (h *CreateWorkHandler) defaultThinkingLevel(model domain.WorkModelSelection, defaults domain.WorkDefaults) string {
	levels, templateDefault, _ := h.models.ThinkingLevels(model)
	if defaults.ThinkingLevel != "" && slices.Contains(levels, defaults.ThinkingLevel) {
		return defaults.ThinkingLevel
	}
	return templateDefault
}

func validateInitialThinkingLevel(models query.ModelConfigurations, model domain.WorkModelSelection, level string) error {
	if level == "" {
		return nil
	}
	if levels, _, ok := models.ThinkingLevels(model); ok && slices.Contains(levels, level) {
		return nil
	}
	return fmt.Errorf("%w: thinking level is not available for this Work model", domain.ErrInvalidWork)
}
