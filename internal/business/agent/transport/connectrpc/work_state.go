package connectrpc

import (
	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WorkStateFrame is the Work's current state as its stream readers receive
// it; startup facts use the same projection as the authorized work query.
func WorkStateFrame(state query.WorkView) *streamgatewaypublicv1.WorkStateFrame {
	frame := &streamgatewaypublicv1.WorkStateFrame{AgentWorkId: state.WorkID, State: state.State, StateReason: state.StateReason}
	if progress := state.Progress; progress != nil {
		frame.WorkProgress = &streamgatewaypublicv1.WorkProgress{UpdatedAt: timestamppb.New(progress.UpdatedAt)}
		for _, item := range progress.Items {
			frame.WorkProgress.Items = append(frame.WorkProgress.Items, &streamgatewaypublicv1.WorkProgressItem{Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm})
		}
	}
	if progress := state.StartupProgress; progress != nil {
		frame.PreparationProgress = &agentpublicv1.StartupProgress{RequestId: progress.RequestID, Admitted: progress.Admitted, Revision: progress.Revision, Status: progress.Status, Reason: progress.Reason}
		for _, stage := range progress.Stages {
			frame.PreparationProgress.Stages = append(frame.PreparationProgress.Stages, &agentpublicv1.StartupStage{Stage: stage.Stage, Status: stage.Status})
		}
	}
	for _, agent := range state.SubAgents {
		frame.Subagents = append(frame.Subagents, &agentpublicv1.SubAgent{Id: agent.ID, Type: agent.Type, Task: agent.Task, Status: agent.Status})
	}
	if !state.UpdatedAt.IsZero() {
		frame.UpdatedAt = timestamppb.New(state.UpdatedAt)
	}
	return frame
}
