package connectrpc

import (
	"time"

	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func mapPortalLivePayload(payload query.PortalLivePayload) *streamgatewaypublicv1.WatchStreamResponse {
	switch p := payload.(type) {
	case *query.UnifiedMessagePayload:
		if p.Message == nil {
			return nil
		}
		return &streamgatewaypublicv1.WatchStreamResponse{
			Replay:  p.Replay,
			Payload: &streamgatewaypublicv1.WatchStreamResponse_Message{Message: messageToProto(p.Message)},
		}
	case *query.WorkStatePayload:
		if p.Frame == nil {
			return nil
		}
		return &streamgatewaypublicv1.WatchStreamResponse{
			Payload: &streamgatewaypublicv1.WatchStreamResponse_WorkState{WorkState: workStateToProto(p.Frame)},
		}
	case *query.DiagnosticPayload:
		if p.Frame == nil {
			return nil
		}
		return &streamgatewaypublicv1.WatchStreamResponse{
			Payload: &streamgatewaypublicv1.WatchStreamResponse_Diagnostic{Diagnostic: diagnosticToProto(p.Frame)},
		}
	case *query.AccessWithdrawnPayload:
		return &streamgatewaypublicv1.WatchStreamResponse{
			Payload: &streamgatewaypublicv1.WatchStreamResponse_AccessWithdrawn{AccessWithdrawn: &streamgatewaypublicv1.AccessWithdrawnFrame{AgentWorkId: p.AgentWorkID}},
		}
	default:
		return nil
	}
}

func optionalTimestamp(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}
func workStateToProto(state *query.WorkState) *streamgatewaypublicv1.WorkStateFrame {
	frame := &streamgatewaypublicv1.WorkStateFrame{AgentWorkId: state.AgentWorkID, State: state.State,
		StateReason: state.StateReason, UpdatedAt: optionalTimestamp(state.UpdatedAt)}
	if p := state.PreparationProgress; p != nil {
		frame.PreparationProgress = &agentpublicv1.StartupProgress{RequestId: p.RequestID, Admitted: p.Admitted, Revision: p.Revision, Status: p.Status, Reason: p.Reason}
		for _, s := range p.Stages {
			frame.PreparationProgress.Stages = append(frame.PreparationProgress.Stages, &agentpublicv1.StartupStage{Stage: s.Stage, Status: s.Status})
		}
	}
	if progress := state.WorkProgress; progress != nil {
		frame.WorkProgress = &streamgatewaypublicv1.WorkProgress{UpdatedAt: optionalTimestamp(progress.UpdatedAt), Items: make([]*streamgatewaypublicv1.WorkProgressItem, 0, len(progress.Items))}
		for _, item := range progress.Items {
			frame.WorkProgress.Items = append(frame.WorkProgress.Items, &streamgatewaypublicv1.WorkProgressItem{
				Id: item.ID, Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm,
			})
		}
	}
	for _, agent := range state.SubAgents {
		frame.Subagents = append(frame.Subagents, &agentpublicv1.SubAgent{Id: agent.ID, Type: agent.Type, Task: agent.Task, Status: agent.Status})
	}
	return frame
}
func diagnosticToProto(frame *query.Diagnostic) *streamgatewaypublicv1.StreamDiagnosticFrame {
	return &streamgatewaypublicv1.StreamDiagnosticFrame{AgentWorkId: frame.AgentWorkID, Severity: frame.Severity, Reason: frame.Reason, Message: frame.Message, OccurredAt: optionalTimestamp(frame.OccurredAt)}
}
