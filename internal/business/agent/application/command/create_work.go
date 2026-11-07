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
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

var (
	// ErrWorkCreationConflict refuses a repeated creation request identity
	// that carries other content than its original request.
	ErrWorkCreationConflict = fmt.Errorf("%w: Work creation request has different immutable content", ErrStartRequestConflict)
	// ErrWorkCreationCanceled answers a creation request identity its caller
	// cancelled, or that a definitive refusal settled: it creates nothing.
	ErrWorkCreationCanceled = fmt.Errorf("%w: Work creation request was cancelled", ErrStartRequestCanceled)
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
	// Subject is the optional business object the Work is about; ProjectID
	// is its optional Project.
	Subject               domain.WorkSubject
	ProjectID             string
	WorkspaceReference    string
	InputSource           domain.WorkInputSource
	ReadOnlyMounts        []domain.ReadOnlyMount `json:",omitempty"`
	TraceAttributes       map[string]string
	CapabilityDifferences domain.CapabilityDifferences
	// StartingContext, when supplied, starts a new Work from another Work of
	// the same definition; a reused Work keeps its own.
	StartingContext *domain.StartingContextSource
	// WorkID, when supplied, is the identity the caller allocated; a Personal
	// Work's File Space is opened under it before the Work exists. Otherwise
	// the creation request identity names the Work.
	WorkID string
	// DisplayTitle is a Personal Work's initial display title.
	DisplayTitle string
	// ComputerID is the Computer its starter chose; empty runs it on the
	// default Computer (see workComputer).
	ComputerID string
	// Harness is the starter's choice for the Work's Harness, admitted only
	// on a Managed Computer.
	Harness domain.HarnessChoice
}

// WorkRegistration is the creation request a new Work is registered with:
// its request record, its creation correlation and the definition revision
// used for its creation.
type WorkRegistration struct {
	SubjectID string
	Request   query.StartRequest
	// WorkspaceReference is the one the creating caller named, part of its
	// creation correlation even for a Work that keeps no Session Workspace.
	WorkspaceReference string
	DefinitionVersion  int
}

// CreateWorkHandler creates at most one Work per caller request identity: it
// derives the Work and its first Desk from that identity, asks the Computer
// for admission, and commits the Work with its request record in one
// transaction. A repeat returns the original outcome, a definitive refusal
// settles the record, and a request its caller cancelled creates nothing.
// The caller owns the identity, its admission and its recovery.
type CreateWorkHandler struct {
	now           func() time.Time
	catalog       StartCatalogPort
	computers     ComputerPort
	models        query.ModelConfigurations
	agents        domain.AgentDefinitionRepository
	sessions      AgentWorkRepository
	authorization access.WorkAuthorizer
	prepare       *PrepareWorkExecutionHandler
}

func NewCreateWorkHandler(now func() time.Time, agents domain.AgentDefinitionRepository, sessions AgentWorkRepository, authorization access.WorkAuthorizer, models query.ModelConfigurations, catalog StartCatalogPort, computers ComputerPort, prepare *PrepareWorkExecutionHandler) *CreateWorkHandler {
	return &CreateWorkHandler{now: now, catalog: catalog, computers: computers, agents: agents, sessions: sessions, authorization: authorization, models: models, prepare: prepare}
}

// creationAttempts bounds retries after a competing change of the
// definition or of the request's own Work between reading and committing.
const creationAttempts = 8

func (h *CreateWorkHandler) Handle(ctx context.Context, request CreateWork) (string, error) {
	request, err := normalizeCreation(request)
	if err != nil {
		return "", err
	}
	fingerprint, err := requestFingerprint(request)
	if err != nil {
		return "", err
	}
	for range creationAttempts {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Read on every attempt: a competing creation of the same request
		// identity that committed first is found here.
		record, found, err := h.sessions.FindStartRequest(ctx, request.RequestID)
		if err != nil {
			return "", err
		}
		if found {
			workID, err := repeatedCreation(record, fingerprint)
			if err != nil {
				return "", err
			}
			return h.created(ctx, request, workID)
		}
		workID, err := h.create(ctx, request, fingerprint)
		if errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		if err != nil && definitiveCreationFailure(err) {
			return h.settle(ctx, request, fingerprint, err)
		}
		return workID, err
	}
	return "", domain.ErrConcurrentModification
}

// Cancel settles an unanswered request identity for its caller: it returns the
// Work the request already created, or fences the identity so a delayed
// repeat creates nothing.
func (h *CreateWorkHandler) Cancel(ctx context.Context, requestID string) (string, error) {
	if uuid.Validate(requestID) != nil {
		return "", query.ErrInvalidStartRequest
	}
	record, err := h.sessions.RecordCreation(ctx, query.StartRequest{ID: requestID, Kind: query.StartKindStart, Canceled: true})
	if err != nil {
		return "", err
	}
	if !record.OfCreation() {
		return "", ErrStartRequestConflict
	}
	return record.WorkID, nil
}

// Outcome reports a creation request identity's record without advancing it:
// its Work or its definitive cancellation. found is false for an identity
// Agent has not recorded, or one that names another operation.
func (h *CreateWorkHandler) Outcome(ctx context.Context, requestID string) (query.StartRequest, bool, error) {
	if uuid.Validate(requestID) != nil {
		return query.StartRequest{}, false, query.ErrInvalidStartRequest
	}
	record, found, err := h.sessions.FindStartRequest(ctx, requestID)
	if err != nil || !found || !record.OfCreation() {
		return query.StartRequest{}, false, err
	}
	return record, true, nil
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
		return request, query.ErrInvalidStartRequest
	}
	return request, nil
}

// repeatedCreation answers a creation request identity already recorded.
func repeatedCreation(record query.StartRequest, fingerprint string) (string, error) {
	if err := AnswerRetained(record, query.StartRequest{ID: record.ID, Kind: query.StartKindStart, Fingerprint: fingerprint}); err != nil {
		return "", err
	}
	return record.WorkID, nil
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

// reused answers a request whose Subject's Work already existed and records
// that outcome on its request identity, so a repeat, the creation outcome
// query and a late cancellation all find the Work it answered.
func (h *CreateWorkHandler) reused(ctx context.Context, request CreateWork, fingerprint, workID string) (string, error) {
	id, err := h.created(ctx, request, workID)
	if err != nil {
		return "", err
	}
	outcome := query.StartRequest{ID: request.RequestID, Kind: query.StartKindStart, WorkID: workID, Fingerprint: fingerprint}
	record, err := h.sessions.RecordCreation(ctx, outcome)
	if err != nil {
		return "", err
	}
	if err := AnswerRetained(record, outcome); err != nil {
		return "", err
	}
	return id, nil
}

// settle records a definitive refusal on the request identity, so a repeat
// answers cancelled rather than retrying, unless a Work already won it.
func (h *CreateWorkHandler) settle(ctx context.Context, request CreateWork, fingerprint string, cause error) (string, error) {
	record, err := h.sessions.RecordCreation(ctx, query.StartRequest{ID: request.RequestID, Kind: query.StartKindStart, Fingerprint: fingerprint, Canceled: true})
	if err != nil {
		return "", errors.Join(cause, err)
	}
	if record.Admitted() && record.Kind == query.StartKindStart {
		return h.created(ctx, request, record.WorkID)
	}
	return "", errors.Join(cause, ErrWorkCreationCanceled)
}

func definitiveCreationFailure(err error) bool {
	return errors.Is(err, domain.ErrCapacityUnavailable) || domain.RefusedByComputer(err) || errors.Is(err, ErrDeskUnavailable) || errors.Is(err, domain.ErrModelConfigurationWithdrawn) || errors.Is(err, pluginconfig.ErrInvalidConfiguration) || errors.Is(err, access.ErrAccessDenied) || errors.Is(err, domain.ErrAgentDefinitionNotFound) || errors.Is(err, domain.ErrInvalidWork) || errors.Is(err, domain.ErrInvalidWorkSubject) || errors.Is(err, domain.ErrInvalidCapabilities)
}

// workModel is the model a new Work retains: the requested one, or, with none
// requested, the machine's Claude login on a Managed Computer and the first
// provided Model Configuration elsewhere.
func (h *CreateWorkHandler) workModel(requested domain.WorkModelSelection, managed bool) domain.WorkModelSelection {
	if requested != (domain.WorkModelSelection{}) {
		return requested
	}
	if managed {
		return domain.MachineLoginModel()
	}
	if option, ok := h.models.Default(); ok {
		return option.Selection
	}
	return requested
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
