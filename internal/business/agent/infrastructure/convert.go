package infrastructure

import (
	"encoding/json"
	"time"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

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
	capabilities, err := encodeCapabilities(agent.Capabilities, agent.WorkDefaults)
	if err != nil {
		return nil, err
	}
	owners, err := encode(agent.Owners)
	if err != nil {
		return nil, err
	}
	layout, err := encodeWorkLayout(agent.WorkLayout)
	if err != nil {
		return nil, err
	}
	return &agentDefinitionDO{Owners: owners, AgentName: agent.AgentName, IconGlyph: agent.Icon.Glyph, IconColor: agent.Icon.Color, Purpose: agent.Purpose, ScopeKind: agent.UsageScope.Kind, ScopeRef: agent.UsageScope.Reference, WorkLayout: layout, VisibilityPublic: agent.Visibility.Public, ID: agent.ID, Capabilities: capabilities, Soul: agent.Soul, ApplicationRemoved: agent.ApplicationRemoved, Version: agent.Version}, nil
}

func agentDefinitionFromDO(row *agentDefinitionDO) (*domain.AgentDefinition, error) {
	capabilities, defaults, err := decodeCapabilities(row.Capabilities)
	if err != nil {
		return nil, err
	}
	var owners []string
	if err := json.Unmarshal([]byte(row.Owners), &owners); err != nil {
		return nil, oops.Wrap(err)
	}
	layout, err := decodeWorkLayout(row.WorkLayout)
	if err != nil {
		return nil, err
	}
	agent := &domain.AgentDefinition{Owners: owners, AgentName: row.AgentName, Icon: domain.AgentIcon{Glyph: row.IconGlyph, Color: row.IconColor}, Purpose: row.Purpose, UsageScope: domain.UsageScope{Kind: row.ScopeKind, Reference: row.ScopeRef}, WorkLayout: layout, Visibility: domain.VisibilityRule{Public: row.VisibilityPublic}, ID: row.ID, Capabilities: capabilities, WorkDefaults: defaults, Soul: row.Soul, ApplicationRemoved: row.ApplicationRemoved, Version: row.Version}
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
	checkouts, err := encodeRepositoryCheckouts(session.RepositoryCheckouts)
	if err != nil {
		return nil, err
	}
	initial, err := encodeInitialTaskInput(session.InitialTaskInput)
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
	layout, err := encodeLocatedLayout(session.LocatedLayout)
	if err != nil {
		return nil, err
	}
	row := &agentWorkDO{LocatedLayout: layout, AgentHarness: session.AgentHarnessReference, DisplayTitle: session.DisplayTitle, ProjectRef: session.ProjectID, VisibilityPublic: session.Visibility.Public, StartedBy: session.StartedBy, ComputerID: session.ComputerID, ComputerKind: string(session.ComputerKind), RepositorySelection: repositories, RepositoryCheckouts: checkouts, InitialTaskInput: initial, AuthorizationSelection: authorizations, ThinkingLevel: session.ThinkingLevel, PermissionMode: string(session.PermissionMode), WorkProgress: progress, ModelConfigurationID: session.WorkModel.ConfigurationID, ModelMachineLogin: session.WorkModel.MachineLogin, ID: session.ID, AgentID: session.AgentID, State: state, Capabilities: capabilities, ContextRef: session.HarnessSessionReference, StartingContext: starting, RepresentedPerson: session.RepresentedPerson, TraceAttributes: attributes, HarnessProcess: current, HoldsProcess: session.HoldsProcess(), StopRequest: session.StopRequestReference, CloseRequest: session.CloseRequestReference, StopReason: string(session.StopReason), DisconnectedSince: unixMillis(session.DisconnectedSince), ReachResumeDue: session.ReachResumeDue, ReachResumeFailures: session.ReachResumeFailures, EndRequest: session.EndRequestReference, FailureReason: session.FailureReason, Version: session.Version}
	if session.Subject.Present() {
		kind, id := string(session.Subject.Kind), session.Subject.ID
		row.SubjectKind, row.SubjectID = &kind, &id
	}
	return row, nil
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
	checkouts, err := decodeRepositoryCheckouts(row.RepositoryCheckouts)
	if err != nil {
		return nil, err
	}
	initial, err := decodeInitialTaskInput(row.InitialTaskInput)
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
	session := &domain.AgentWork{DisplayTitle: row.DisplayTitle, StartedBy: row.StartedBy, ComputerID: row.ComputerID, ComputerKind: domain.ComputerKind(row.ComputerKind), RepositorySelection: repositories, RepositoryCheckouts: checkouts, InitialTaskInput: initial, AuthorizationSelection: authorizations, ThinkingLevel: row.ThinkingLevel, PermissionMode: domain.PermissionMode(row.PermissionMode), WorkModel: domain.WorkModelSelection{ConfigurationID: row.ModelConfigurationID, MachineLogin: row.ModelMachineLogin}, Subject: subjectFromColumns(row.SubjectKind, row.SubjectID), ProjectID: row.ProjectRef, ID: row.ID, AgentID: row.AgentID, State: sessionStates[row.State], CapabilitySnapshot: capabilities, HarnessSessionReference: row.ContextRef, AgentHarnessReference: row.AgentHarness, RepresentedPerson: row.RepresentedPerson, StartingContext: starting, StopRequestReference: row.StopRequest, CloseRequestReference: row.CloseRequest, StopReason: domain.StopReason(row.StopReason), DisconnectedSince: fromUnixMillis(row.DisconnectedSince), ReachResumeDue: row.ReachResumeDue, ReachResumeFailures: row.ReachResumeFailures, EndRequestReference: row.EndRequest, FailureReason: row.FailureReason, Visibility: domain.WorkVisibility{Public: row.VisibilityPublic}, Events: domain.NewEventCollection(), Version: row.Version}
	if err := json.Unmarshal([]byte(row.TraceAttributes), &session.TraceAttributes); err != nil {
		return nil, err
	}
	if session.LocatedLayout, err = decodeLocatedLayout(row.LocatedLayout); err != nil {
		return nil, err
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
	if current != nil {
		session.HarnessProcess = &domain.HarnessProcess{PhaseSequence: current.PhaseSequence, WorkProgressSequence: current.WorkProgressSequence, ContextMode: domain.ContextMode(current.ContextMode), DeskID: current.DeskID, DeskWaiting: current.DeskWaiting, StartWithdrawn: current.StartWithdrawn, Soul: current.Soul, Ready: current.Ready, IdleSince: current.IdleSince, WaitingSince: current.WaitingSince, StartingSince: current.StartingSince, Exited: current.Exited, ComputeReclaimed: current.ComputeReclaimed, EnvironmentLost: current.EnvironmentLost, StartRejected: current.StartRejected, StartAbandoned: current.StartAbandoned, Unreached: current.Unreached, Fenced: current.Fenced, Turn: domain.TurnObservation{Sequence: current.Turn.Sequence, Active: current.Turn.Active, Completed: current.Turn.Completed}, SubAgentsSequence: current.SubAgentsSequence, PermissionModeSequence: current.PermissionModeSequence}
		for _, agent := range current.SubAgents {
			session.HarnessProcess.SubAgents = append(session.HarnessProcess.SubAgents, domain.SubAgent{ID: agent.ID, Type: agent.Type, Task: agent.Task, Status: domain.SubAgentStatus(agent.Status)})
		}
	}
	return session, session.Validate()
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
