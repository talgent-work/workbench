package connectrpc

import (
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func workProgressToProto(progress *query.WorkProgress) *agentpublicv1.WorkProgress {
	if progress == nil {
		return nil
	}
	result := &agentpublicv1.WorkProgress{UpdatedAt: timestamppb.New(progress.UpdatedAt)}
	for _, item := range progress.Items {
		result.Items = append(result.Items, &agentpublicv1.WorkProgressItem{Content: item.Content, Status: item.Status, ActiveForm: item.ActiveForm})
	}
	return result
}
