package infrastructure

import (
	"time"

	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func optionalTime(value *timestamppb.Timestamp) *time.Time {
	if value == nil {
		return nil
	}
	t := value.AsTime()
	return &t
}
func workStateFromProto(frame *streamgatewaypublicv1.WorkStateFrame) *query.WorkState {
	state := &query.WorkState{AgentWorkID: frame.AgentWorkId, State: frame.State,
		StateReason: frame.StateReason, UpdatedAt: optionalTime(frame.UpdatedAt)}
	if p := frame.PreparationProgress; p != nil {
		state.PreparationProgress = &query.StartupProgress{RequestID: p.RequestId, Admitted: p.Admitted, Revision: p.Revision, Status: p.Status, Reason: p.Reason}
		for _, s := range p.Stages {
			state.PreparationProgress.Stages = append(state.PreparationProgress.Stages, query.StartupStage{Stage: s.GetStage(), Status: s.GetStatus()})
		}
	}
	if progress := frame.WorkProgress; progress != nil {
		state.WorkProgress = &query.WorkProgress{UpdatedAt: optionalTime(progress.UpdatedAt), Items: make([]query.WorkProgressItem, 0, len(progress.Items))}
		for _, item := range progress.Items {
			state.WorkProgress.Items = append(state.WorkProgress.Items, query.WorkProgressItem{
				ID: item.GetId(), Content: item.GetContent(), Status: item.GetStatus(), ActiveForm: item.GetActiveForm(),
			})
		}
	}
	for _, agent := range frame.GetSubagents() {
		state.SubAgents = append(state.SubAgents, query.SubAgent{ID: agent.GetId(), Type: agent.GetType(), Task: agent.GetTask(), Status: agent.GetStatus()})
	}
	return state
}
func diagnosticFromProto(frame *streamgatewaypublicv1.StreamDiagnosticFrame) *query.Diagnostic {
	return &query.Diagnostic{AgentWorkID: frame.AgentWorkId, Severity: frame.Severity, Reason: frame.Reason, Message: frame.Message, OccurredAt: optionalTime(frame.OccurredAt)}
}

func workAccessFromProto(frame *streamgatewaypublicv1.WorkAccessFrame) *query.WorkAccessPayload {
	if private := frame.GetMadePrivate(); private != nil {
		return &query.WorkAccessPayload{MadePrivate: true, RetainedReaderIDs: private.GetReaderIds()}
	}
	return &query.WorkAccessPayload{RevokedReaderID: frame.GetRevokedReaderId()}
}
