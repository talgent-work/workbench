package infrastructure

import (
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type workStateHistoryDO struct {
	ID            string `xorm:"id pk"`
	SessionID     string `xorm:"session_id"`
	SourceVersion int    `xorm:"source_version"`
	State         uint8  `xorm:"state"`
	Started       bool   `xorm:"started"`
	Failed        bool   `xorm:"failed"`
	Waiting       bool   `xorm:"waiting"`
	ObservedAt    int64  `xorm:"observed_at"`
	Version       int    `xorm:"version"`
	CreatedAt     int64  `xorm:"created_at"`
	UpdatedAt     int64  `xorm:"updated_at"`
	DeletedAt     int64  `xorm:"deleted_at"`
}

func (workStateHistoryDO) TableName() string { return "agent_session_state_history" }

type agentDefinitionDO struct {
	Owners           string `xorm:"owners JSON"`
	AgentName        string `xorm:"agent_name varchar(512)"`
	IconGlyph        string `xorm:"icon_glyph varchar(32)"`
	IconColor        string `xorm:"icon_color varchar(32)"`
	Purpose          string `xorm:"purpose varchar(64)"`
	ScopeKind        string `xorm:"scope_kind varchar(64)"`
	ScopeRef         string `xorm:"scope_ref varchar(255)"`
	WorkLayout       string `xorm:"work_layout JSON"`
	VisibilityPublic bool   `xorm:"visibility_public"`
	ID               string `xorm:"id pk varchar(36)"`
	Capabilities     string `xorm:"capabilities text"`
	Soul             string `xorm:"soul text"`
	Version          int    `xorm:"'version' int"`
	CreatedAt        int64  `xorm:"created_at bigint"`
	UpdatedAt        int64  `xorm:"updated_at bigint"`
	DeletedAt        int64  `xorm:"deleted_at bigint"`
}

func (agentDefinitionDO) TableName() string { return "agent_definition" }

type agentWorkDO struct {
	RepositorySelection string `xorm:"repository_selection json"`
	// RepositoryCheckouts is the JSON of the selected repositories' final
	// checkout outcomes.
	RepositoryCheckouts    string `xorm:"repository_checkouts json"`
	AuthorizationSelection string `xorm:"authorization_selection json"`
	ThinkingLevel          string `xorm:"thinking_level varchar(32)"`
	PermissionMode         string `xorm:"permission_mode varchar(32)"`
	WorkProgress           string `xorm:"work_progress mediumtext"`
	// InitialTaskInput is the JSON of the initial task input the Work still
	// holds; empty once handed off or for a Work created without one.
	InitialTaskInput     string `xorm:"initial_task_input mediumtext"`
	ModelConfigurationID string `xorm:"model_configuration_id varchar(128)"`
	ModelMachineLogin    bool   `xorm:"model_machine_login"`
	// SubjectKind and SubjectID are NULL for a Work without a Subject, so the
	// unique index binds only Works with one.
	SubjectKind     *string `xorm:"subject_kind varchar(32)"`
	SubjectID       *string `xorm:"subject_id varchar(255)"`
	ProjectRef      string  `xorm:"project_ref varchar(255)"`
	StartedBy       string  `xorm:"started_by varchar(255)"`
	ComputerID      string  `xorm:"computer_id varchar(36)"`
	ComputerKind    string  `xorm:"computer_kind varchar(16)"`
	ID              string  `xorm:"id pk varchar(36)"`
	AgentID         string  `xorm:"agent_id varchar(36)"`
	State           uint8   `xorm:"state tinyint"`
	Capabilities    string  `xorm:"capabilities text"`
	ContextRef      string  `xorm:"context_ref varchar(36)"`
	AgentHarness    string  `xorm:"agent_harness varchar(64)"`
	LocatedLayout   string  `xorm:"located_layout JSON"`
	DisplayTitle    string  `xorm:"display_title varchar(200)"`
	StartingContext string  `xorm:"starting_context"`
	// RepresentedPerson is the person the Work's Agent represents; empty for
	// one that represents no person.
	RepresentedPerson string `xorm:"represented_person varchar(255)"`
	// OwningApplication is the Application that owns the Work; empty for
	// one no Application owns.
	OwningApplication string `xorm:"owning_application varchar(36)"`
	TraceAttributes   string `xorm:"trace_attributes JSON"`
	HarnessProcess    string `xorm:"harness_process text"`
	HoldsProcess      bool   `xorm:"holds_process"`
	StopReason        string `xorm:"stop_reason"`
	// DisconnectedSince is the Disconnection origin in Unix milliseconds;
	// zero while the Work's execution environment is observed.
	DisconnectedSince int64 `xorm:"disconnected_since bigint"`
	// ReachResumeDue marks a Resume refused while the Work's Managed
	// Computer was unreachable, retried once on its reconnection.
	ReachResumeDue bool `xorm:"reach_resume_due"`
	// ReachResumeFailures counts the retries of the owed Resume that failed
	// without an answer.
	ReachResumeFailures int    `xorm:"reach_resume_failures"`
	StopRequest         string `xorm:"stop_request varchar(128)"`
	CloseRequest        string `xorm:"close_request varchar(128)"`
	EndRequest          string `xorm:"end_request"`
	FailureReason       string `xorm:"failure_reason text"`
	VisibilityPublic    bool   `xorm:"visibility_public"`
	Version             int    `xorm:"'version' int"`
	CreatedAt           int64  `xorm:"created_at bigint"`
	UpdatedAt           int64  `xorm:"updated_at bigint"`
	DeletedAt           int64  `xorm:"deleted_at bigint"`
}

func (agentWorkDO) TableName() string { return "agent_session" }

// agentWorkShareDO is one Work Share: a reader the Work's owner lets read its
// private message stream, in grant order.
type agentWorkShareDO struct {
	WorkID    string `xorm:"work_id pk varchar(36)"`
	ReaderID  string `xorm:"reader_id pk varchar(255)"`
	Position  int    `xorm:"position int"`
	CreatedAt int64  `xorm:"created_at bigint"`
}

func (agentWorkShareDO) TableName() string { return "agent_work_share" }

type harnessProcessRecord struct {
	PhaseSequence          int64            `json:"phase_sequence"`
	WorkProgressSequence   int64            `json:"work_progress_sequence"`
	SubAgentsSequence      int64            `json:"subagents_sequence,omitempty"`
	PermissionModeSequence int64            `json:"permission_mode_sequence,omitempty"`
	SubAgents              []subAgentRecord `json:"subagents,omitempty"`
	IdleSince              time.Time        `json:"idle_since"`
	WaitingSince           time.Time        `json:"waiting_since"`
	StartingSince          time.Time        `json:"starting_since"`
	ContextMode            string           `json:"context_mode"`
	DeskID                 string           `json:"desk_id,omitempty"`
	DeskWaiting            bool             `json:"desk_waiting,omitempty"`
	StartWithdrawn         bool             `json:"start_withdrawn,omitempty"`
	Soul                   string           `json:"soul,omitempty"`
	// Ready records the run's readiness its AgentExecution reported.
	Ready            bool                  `json:"ready"`
	Exited           bool                  `json:"exited"`
	ComputeReclaimed bool                  `json:"compute_reclaimed"`
	StartRejected    bool                  `json:"start_rejected"`
	StartAbandoned   bool                  `json:"start_abandoned,omitempty"`
	Unreached        bool                  `json:"unreached,omitempty"`
	Fenced           bool                  `json:"fenced,omitempty"`
	EnvironmentLost  bool                  `json:"environment_lost"`
	Turn             turnObservationRecord `json:"turn"`
}

// waiting reports a stored Work whose user owes an answer; closure clears it.
func (r *harnessProcessRecord) waiting() bool { return !r.WaitingSince.IsZero() }

// initial reports whether the process is the Work's first start, which its
// creation admitted.
func (r *harnessProcessRecord) initial() bool {
	return r.ContextMode == string(domain.ContextFresh)
}

// startIdentity is the identity a view gives the start: a creation request
// derives the Work, so the first start is named by the Work, and a Resume or
// Restart request derives its Desk, which names that start.
func (r *harnessProcessRecord) startIdentity(workID string) string {
	if r.initial() || r.DeskID == "" {
		return workID
	}
	return r.DeskID
}

type subAgentRecord struct {
	ID     string `json:"id"`
	Type   string `json:"type,omitempty"`
	Task   string `json:"task,omitempty"`
	Status string `json:"status"`
}

type turnObservationRecord struct {
	Sequence  int64 `json:"sequence"`
	Active    bool  `json:"active"`
	Completed bool  `json:"completed"`
}

type workProgressRecord struct {
	Items     []workProgressItemRecord `json:"items"`
	UpdatedAt time.Time                `json:"updated_at"`
}

type workProgressItemRecord struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}
