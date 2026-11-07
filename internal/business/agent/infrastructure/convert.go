package infrastructure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

func workCreationToDO(request command.WorkCreationRequest) (workCreationDO, error) {
	initial := request.Creation.InitialExecution
	if uuid.Validate(request.ID) != nil || initial == nil || initial.CreationRequestID != request.ID || initial.RequestID != request.ID || initial.WorkID != "" || initial.SubjectID != request.Creation.SubjectID || initial.WorkspaceReference != request.Creation.WorkspaceReference || request.WorkID != "" || request.Canceled || request.BindingConfirmed {
		return workCreationDO{}, command.ErrWorkCreationConflict
	}
	payload, err := encode(request.Creation)
	if err != nil {
		return workCreationDO{}, err
	}
	digest, err := subjectDigest(request.Creation.AgentID, request.Creation.Subject)
	if err != nil {
		return workCreationDO{}, err
	}
	return workCreationDO{ID: request.ID, SubjectDigest: digest, RequestData: payload, Pending: true}, nil
}

// cancelledCreationData marks an identity cancelled before Agent received its
// request; it carries no creation.
const cancelledCreationData = "{}"

func workCreationFromDO(row workCreationDO) (command.WorkCreationRequest, error) {
	if row.RequestData == cancelledCreationData {
		if !row.Canceled || row.SessionID != "" {
			return command.WorkCreationRequest{}, command.ErrWorkCreationConflict
		}
		return command.WorkCreationRequest{ID: row.ID, Canceled: true}, nil
	}
	var creation command.CreateWork
	if err := json.Unmarshal([]byte(row.RequestData), &creation); err != nil {
		return command.WorkCreationRequest{}, oops.Wrap(err)
	}
	for i := range creation.CapabilityDifferences.Plugins {
		choice := &creation.CapabilityDifferences.Plugins[i]
		choice.Configuration = pluginconfig.FromJSON(choice.Configuration)
	}
	if _, err := workCreationToDO(command.WorkCreationRequest{ID: row.ID, Creation: creation}); err != nil {
		return command.WorkCreationRequest{}, err
	}
	if (row.Canceled && row.SessionID != "") || (row.BindingConfirmed && row.SessionID == "") {
		return command.WorkCreationRequest{}, command.ErrWorkCreationConflict
	}
	return command.WorkCreationRequest{ID: row.ID, WorkID: row.SessionID, Creation: creation, Canceled: row.Canceled, BindingConfirmed: row.BindingConfirmed}, nil
}

// subjectDigest correlates a creation request with the Work its definition
// holds for the same Subject, without interpreting the Subject; a request
// without a Subject correlates by its own identity and has none.
func subjectDigest(agentID string, subject domain.WorkSubject) (string, error) {
	if !subject.Present() {
		return "", nil
	}
	data, err := json.Marshal(struct {
		Agent   string
		Subject domain.WorkSubject
	}{agentID, subject})
	if err != nil {
		return "", oops.Wrap(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func initialExecutionRequestFromDO(row initialExecutionRequestDO) (command.InitialExecutionRequest, error) {
	var request command.InitialExecutionRequest
	if err := json.Unmarshal([]byte(row.RequestData), &request); err != nil {
		return request, oops.Wrap(err)
	}
	if request.WorkID != row.ID || request.RequestID != row.RequestID {
		return command.InitialExecutionRequest{}, command.ErrInitialExecutionConflict
	}
	for _, selection := range request.Selections {
		if selection.RepositoryID == "" {
			return command.InitialExecutionRequest{}, command.ErrInitialExecutionConflict
		}
	}
	request.Version = row.Version
	return request, nil
}

func initialExecutionRequestToDO(request command.InitialExecutionRequest) (initialExecutionRequestDO, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return initialExecutionRequestDO{}, oops.Wrap(err)
	}
	return initialExecutionRequestDO{ID: request.WorkID, RequestID: request.RequestID, RequestData: string(payload), Submitted: request.Submitted, Pending: !request.FirstStartRecovered() && !request.CleanupConfirmed && !(request.Rejected && request.RejectionApplied), Version: request.Version}, nil
}

func encode(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", oops.Wrap(err)
	}
	return string(body), nil
}

func agentDefinitionToDO(agent *domain.AgentDefinition) (*agentDefinitionDO, error) {
	if err := agent.Validate(); err != nil {
		return nil, err
	}
	capabilities, err := encodeCapabilities(agent.Capabilities)
	if err != nil {
		return nil, err
	}
	owners, err := encode(agent.Owners)
	if err != nil {
		return nil, err
	}
	return &agentDefinitionDO{Owners: owners, AgentName: agent.AgentName, IconGlyph: agent.Icon.Glyph, IconColor: agent.Icon.Color, Purpose: agent.Purpose, ScopeKind: agent.UsageScope.Kind, ScopeRef: agent.UsageScope.Reference, HomeOwnerID: agent.HomeBinding.OwnerID, HomeNamespace: agent.HomeBinding.Namespace, VisibilityPublic: agent.Visibility.Public, ID: agent.ID, Capabilities: capabilities, Soul: agent.Soul, Version: agent.Version}, nil
}

func agentDefinitionFromDO(row *agentDefinitionDO) (*domain.AgentDefinition, error) {
	capabilities, err := decodeCapabilities(row.Capabilities)
	if err != nil {
		return nil, err
	}
	var owners []string
	if err := json.Unmarshal([]byte(row.Owners), &owners); err != nil {
		return nil, oops.Wrap(err)
	}
	agent := &domain.AgentDefinition{Owners: owners, AgentName: row.AgentName, Icon: domain.AgentIcon{Glyph: row.IconGlyph, Color: row.IconColor}, Purpose: row.Purpose, UsageScope: domain.UsageScope{Kind: row.ScopeKind, Reference: row.ScopeRef}, HomeBinding: domain.HomeBinding{OwnerID: row.HomeOwnerID, Namespace: row.HomeNamespace}, Visibility: domain.VisibilityRule{Public: row.VisibilityPublic}, ID: row.ID, Capabilities: capabilities, Soul: row.Soul, Version: row.Version}
	return agent, agent.Validate()
}

var sessionStates = [...]domain.WorkState{"", domain.WorkStarting, domain.WorkWorking, domain.WorkStopping, domain.WorkStopped, domain.WorkFailed, domain.WorkClosing, domain.WorkClosed}

func sessionToDO(session *domain.AgentWork) (*agentWorkDO, error) {
	if err := session.Validate(); err != nil {
		return nil, err
	}
	capabilities, err := encodeCapabilitySnapshot(session.CapabilitySnapshot)
	if err != nil {
		return nil, err
	}
	starting, err := encodeStartingContext(session.StartingContext)
	if err != nil {
		return nil, err
	}
	repositories, err := encodeWorkRepositories(session.RepositorySelection)
	if err != nil {
		return nil, err
	}
	authorizations, err := encodeWorkAuthorization(session.AuthorizationSelection)
	if err != nil {
		return nil, err
	}
	attributes, err := encode(session.TraceAttributes)
	if err != nil {
		return nil, err
	}
	var process *harnessProcessRecord
	if current := session.HarnessProcess; current != nil {
		process = &harnessProcessRecord{PhaseSequence: current.PhaseSequence, WorkProgressSequence: current.WorkProgressSequence, ContextMode: string(current.ContextMode), DeskID: current.DeskID, DeskWaiting: current.DeskWaiting, StartWithdrawn: current.StartWithdrawn, Soul: current.Soul, Ready: current.Ready, IdleSince: current.IdleSince, WaitingSince: current.WaitingSince, StartingSince: current.StartingSince, Exited: current.Exited, ComputeReclaimed: current.ComputeReclaimed, EnvironmentLost: current.EnvironmentLost, StartRejected: current.StartRejected, StartAbandoned: current.StartAbandoned, Unreached: current.Unreached, Fenced: current.Fenced, Turn: turnObservationRecord{Sequence: current.Turn.Sequence, Active: current.Turn.Active, Completed: current.Turn.Completed}, SubAgentsSequence: current.SubAgentsSequence, PermissionModeSequence: current.PermissionModeSequence}
		for _, agent := range current.SubAgents {
			process.SubAgents = append(process.SubAgents, subAgentRecord{ID: agent.ID, Type: agent.Type, Task: agent.Task, Status: string(agent.Status)})
		}
	}
	current, err := encode(process)
	if err != nil {
		return nil, err
	}
	var state uint8
	for code, candidate := range sessionStates {
		if candidate == session.State {
			state = uint8(code)
			break
		}
	}
	progress, err := encode(workProgressToRecord(session.Progress))
	if err != nil {
		return nil, err
	}
	mounts := make([]readOnlyMountRecord, 0, len(session.ReadOnlyMounts))
	for _, mount := range session.ReadOnlyMounts {
		mounts = append(mounts, readOnlyMountRecord{Name: mount.Name, Reference: mount.Reference})
	}
	readOnlyMounts, err := encode(mounts)
	if err != nil {
		return nil, err
	}
	row := &agentWorkDO{ReadOnlyMounts: readOnlyMounts, DisplayTitle: session.DisplayTitle, ProjectRef: session.ProjectID, VisibilityPublic: session.Visibility.Public, StartedBy: session.StartedBy, ComputerID: session.ComputerID, RepositorySelection: repositories, AuthorizationSelection: authorizations, TaskExecutionBegun: session.TaskExecutionBegun, ThinkingLevel: session.ThinkingLevel, PermissionMode: string(session.PermissionMode), WorkProgress: progress, ModelConfigurationID: session.WorkModel.ConfigurationID, ModelMachineLogin: session.WorkModel.MachineLogin, ID: session.ID, AgentID: session.AgentID, State: state, Capabilities: capabilities, ContextRef: session.HarnessSessionReference, WorkspaceRef: session.WorkspaceReference, InputSource: session.InputSource.Reference, InputSpaceToken: session.InputSource.SpaceToken, StartingContext: starting, HomeOwnerID: session.HomeBinding.OwnerID, HomeNamespace: session.HomeBinding.Namespace, TraceAttributes: attributes, HarnessProcess: current, HoldsProcess: session.HoldsProcess(), StopRequest: session.StopRequestReference, CloseRequest: session.CloseRequestReference, StopReason: string(session.StopReason), DisconnectedSince: unixMillis(session.DisconnectedSince), ReachResumeDue: session.ReachResumeDue, EndRequest: session.EndRequestReference, FailureReason: session.FailureReason, Version: session.Version}
	if session.Subject.Present() {
		kind, id := string(session.Subject.Kind), session.Subject.ID
		row.SubjectKind, row.SubjectID = &kind, &id
	}
	return row, nil
}

// readOnlyMountRecord is one element of agent_session.read_only_mounts.
type readOnlyMountRecord struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

// subjectFromColumns reads a Work Subject whose columns are NULL when absent.
func subjectFromColumns(kind, id *string) domain.WorkSubject {
	if kind == nil || id == nil {
		return domain.WorkSubject{}
	}
	return domain.WorkSubject{Kind: domain.SubjectKind(*kind), ID: *id}
}

func sessionFromDO(row *agentWorkDO) (*domain.AgentWork, error) {
	if int(row.State) >= len(sessionStates) {
		return nil, domain.ErrInvalidWork
	}
	capabilities, err := decodeCapabilitySnapshot(row.Capabilities)
	if err != nil {
		return nil, err
	}
	starting, err := decodeStartingContext(row.StartingContext)
	if err != nil {
		return nil, err
	}
	repositories, err := decodeWorkRepositories(row.RepositorySelection)
	if err != nil {
		return nil, err
	}
	authorizations, err := decodeWorkAuthorization(row.AuthorizationSelection)
	if err != nil {
		return nil, err
	}
	var current *harnessProcessRecord
	if err := json.Unmarshal([]byte(row.HarnessProcess), &current); err != nil {
		return nil, oops.Wrap(err)
	}
	session := &domain.AgentWork{DisplayTitle: row.DisplayTitle, StartedBy: row.StartedBy, ComputerID: row.ComputerID, RepositorySelection: repositories, AuthorizationSelection: authorizations, TaskExecutionBegun: row.TaskExecutionBegun, ThinkingLevel: row.ThinkingLevel, PermissionMode: domain.PermissionMode(row.PermissionMode), WorkModel: domain.WorkModelSelection{ConfigurationID: row.ModelConfigurationID, MachineLogin: row.ModelMachineLogin}, Subject: subjectFromColumns(row.SubjectKind, row.SubjectID), ProjectID: row.ProjectRef, ID: row.ID, AgentID: row.AgentID, State: sessionStates[row.State], CapabilitySnapshot: capabilities, HarnessSessionReference: row.ContextRef, WorkspaceReference: row.WorkspaceRef, InputSource: domain.WorkInputSource{Reference: row.InputSource, SpaceToken: row.InputSpaceToken}, StartingContext: starting, StopRequestReference: row.StopRequest, CloseRequestReference: row.CloseRequest, StopReason: domain.StopReason(row.StopReason), DisconnectedSince: fromUnixMillis(row.DisconnectedSince), ReachResumeDue: row.ReachResumeDue, EndRequestReference: row.EndRequest, FailureReason: row.FailureReason, Visibility: domain.WorkVisibility{Public: row.VisibilityPublic}, Events: domain.NewEventCollection(), Version: row.Version}
	if err := json.Unmarshal([]byte(row.TraceAttributes), &session.TraceAttributes); err != nil {
		return nil, err
	}
	var mounts []readOnlyMountRecord
	if err := json.Unmarshal([]byte(row.ReadOnlyMounts), &mounts); err != nil {
		return nil, oops.Wrap(err)
	}
	for _, mount := range mounts {
		session.ReadOnlyMounts = append(session.ReadOnlyMounts, domain.ReadOnlyMount{Name: mount.Name, Reference: mount.Reference})
	}
	progress, err := decodeWorkProgress(row.WorkProgress)
	if err != nil {
		return nil, err
	}
	if progress != nil {
		session.Progress = &domain.WorkProgress{UpdatedAt: progress.UpdatedAt, Items: make([]domain.WorkProgressItem, len(progress.Items))}
		for i, item := range progress.Items {
			session.Progress.Items[i] = domain.WorkProgressItem{Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm}
		}
	}
	session.HomeBinding = domain.HomeBinding{OwnerID: row.HomeOwnerID, Namespace: row.HomeNamespace}
	if current != nil {
		session.HarnessProcess = &domain.HarnessProcess{PhaseSequence: current.PhaseSequence, WorkProgressSequence: current.WorkProgressSequence, ContextMode: domain.ContextMode(current.ContextMode), DeskID: current.DeskID, DeskWaiting: current.DeskWaiting, StartWithdrawn: current.StartWithdrawn, Soul: current.Soul, Ready: current.Ready, IdleSince: current.IdleSince, WaitingSince: current.WaitingSince, StartingSince: current.StartingSince, Exited: current.Exited, ComputeReclaimed: current.ComputeReclaimed, EnvironmentLost: current.EnvironmentLost, StartRejected: current.StartRejected, StartAbandoned: current.StartAbandoned, Unreached: current.Unreached, Fenced: current.Fenced, Turn: domain.TurnObservation{Sequence: current.Turn.Sequence, Active: current.Turn.Active, Completed: current.Turn.Completed}, SubAgentsSequence: current.SubAgentsSequence, PermissionModeSequence: current.PermissionModeSequence}
		for _, agent := range current.SubAgents {
			session.HarnessProcess.SubAgents = append(session.HarnessProcess.SubAgents, domain.SubAgent{ID: agent.ID, Type: agent.Type, Task: agent.Task, Status: domain.SubAgentStatus(agent.Status)})
		}
	}
	return session, session.Validate()
}

func sessionPreparationFromRequest(request command.InitialExecutionRequest) query.WorkPreparation {
	result := query.WorkPreparation{ThinkingLevel: request.ThinkingLevel, WorkID: request.WorkID, RequestID: request.RequestID, SubjectID: request.SubjectID, WorkBranch: request.WorkBranch, InputID: request.Input.ID, Input: request.Input.Text, Prepared: request.Prepared, Submitted: request.Submitted, InputPrepared: request.InputPrepared, InputBuffered: request.InputBuffered, Rejected: request.Rejected}
	for _, c := range request.Checkouts {
		result.Checkouts = append(result.Checkouts, query.PreparedCheckout{Provider: c.Provider, RepoID: c.RepoID, RepoName: c.RepoName, CloneURL: c.CloneURL, WebURL: c.WebURL, DefaultBranch: c.DefaultBranch, BaseBranch: c.BaseBranch, WorkBranch: c.WorkBranch, TargetPath: c.TargetPath, AccessID: c.AccessID})
	}
	return result
}

func continuationFromDO(row continuationRequestDO) command.ContinuationRequest {
	return command.ContinuationRequest{StartWork: command.StartWork{WorkID: row.ID, SubjectID: row.SubjectID, RequestID: row.RequestID}, Operation: row.Operation, Rejection: row.Rejection, Resolved: !row.Pending}
}

func workProgressToRecord(progress *domain.WorkProgress) *workProgressRecord {
	if progress == nil {
		return nil
	}
	result := &workProgressRecord{UpdatedAt: progress.UpdatedAt, Items: make([]workProgressItemRecord, len(progress.Items))}
	for i, item := range progress.Items {
		result.Items[i] = workProgressItemRecord{Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm}
	}
	return result
}

func decodeWorkProgress(value string) (*workProgressRecord, error) {
	if value == "" {
		return nil, nil
	}
	var result *workProgressRecord
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, oops.Wrap(err)
	}
	return result, nil
}

// unixMillis stores an optional time; zero stays zero.
func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromUnixMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
