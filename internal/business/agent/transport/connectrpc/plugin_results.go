package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/pkg/authn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *WorkHandler) GetWorkPluginResults(ctx context.Context, request *connect.Request[agentpublicv1.GetWorkPluginResultsRequest]) (*connect.Response[agentpublicv1.GetWorkPluginResultsResponse], error) {
	actor, _ := authn.CurrentUserID(ctx)
	results, err := h.application.GetWorkPluginResults(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.Msg.WorkId)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentpublicv1.GetWorkPluginResultsResponse{}
	for _, result := range results {
		item := &agentpublicv1.WorkPluginResult{Slug: result.Slug, ReleaseVersion: result.ReleaseVersion, Source: result.Source, Installation: pluginStatus(result.InstallationObserved, result.Installed), InstallationReason: result.InstallationReason, Loading: pluginStatus(result.LoadObserved, result.Loaded), LoadingReason: result.LoadReason}
		if !result.UpdatedAt.IsZero() {
			item.UpdatedAt = timestamppb.New(result.UpdatedAt)
		}
		response.Plugins = append(response.Plugins, item)
	}
	return connect.NewResponse(response), nil
}

func pluginStatus(observed, succeeded bool) agentpublicv1.PluginPreparationStatus {
	if !observed {
		return agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_UNSPECIFIED
	}
	if succeeded {
		return agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_SUCCEEDED
	}
	return agentpublicv1.PluginPreparationStatus_PLUGIN_PREPARATION_STATUS_FAILED
}
