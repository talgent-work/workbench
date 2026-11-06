package connectrpc

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// WorkFactsHandler serves trusted callers composing Works' collaboration facts.
type WorkFactsHandler struct {
	application *application.Application
	token       string
}

var _ agentprivatev1connect.WorkFactsServiceHandler = (*WorkFactsHandler)(nil)

func NewWorkFactsHandler(app *application.Application, token string) *WorkFactsHandler {
	return &WorkFactsHandler{application: app, token: token}
}

func (h *WorkFactsHandler) ListWorks(ctx context.Context, request *connect.Request[agentprivatev1.ListWorksRequest]) (*connect.Response[agentprivatev1.ListWorksResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	msg := request.Msg
	works, err := h.application.Queries.ListWorks.Handle(ctx, query.ListWorks{Filter: query.WorkListFilter{
		ProjectID: strings.TrimSpace(msg.GetProjectId()), WorkIDs: msg.GetWorkIds(), Purpose: strings.TrimSpace(msg.GetPurpose()), AgentOwner: strings.TrimSpace(msg.GetAgentOwner()),
		AfterID: strings.TrimSpace(msg.GetAfter()), Limit: int(msg.GetLimit()), Recent: msg.GetRecent(), Archived: msg.Archived,
	}})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.ListWorksResponse{}
	for _, work := range works {
		response.Works = append(response.Works, &agentprivatev1.WorkFacts{WorkId: work.WorkID, Subject: subjectToProto(work.Subject), ProjectId: work.ProjectID, AgentOwner: work.AgentOwner, StartedBy: work.StartedBy, State: work.State, StateReason: work.StateReason, ArchivePending: work.ArchivePending, Waiting: work.Waiting, DisconnectedSince: optionalTimestamp(work.DisconnectedSince), CreatedAt: timestamppb.New(work.CreatedAt), UpdatedAt: timestamppb.New(work.UpdatedAt)})
	}
	return connect.NewResponse(response), nil
}

func (h *WorkFactsHandler) GetWorkFacts(ctx context.Context, request *connect.Request[agentprivatev1.GetWorkFactsRequest]) (*connect.Response[agentprivatev1.GetWorkFactsResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	value, err := h.application.GetWorkFacts(ctx, strings.TrimSpace(request.Msg.GetWorkId()))
	if err != nil {
		return nil, mapAgentError(err)
	}
	info := workViewToProto(value)
	facts := &agentprivatev1.WorkFacts{WorkId: value.WorkID, Subject: subjectToProto(value.Subject), ProjectId: value.ProjectID, AgentOwner: value.AgentOwner, StartedBy: value.StartedBy, State: value.State, StateReason: value.StateReason, DiagnosticReason: value.DiagnosticReason,
		WorkProgress: info.WorkProgress, StartupProgress: info.StartupProgress, CreatedAt: info.CreatedAt, UpdatedAt: info.UpdatedAt, Waiting: info.Waiting, ArchivePending: value.ArchivePending, VisibilityPublic: value.Visibility.Public, DeskWaiting: value.DeskWaiting, DisconnectedSince: info.DisconnectedSince}
	return connect.NewResponse(&agentprivatev1.GetWorkFactsResponse{Work: facts}), nil
}

func (h *WorkFactsHandler) GetWorkReport(ctx context.Context, request *connect.Request[agentprivatev1.GetWorkReportRequest]) (*connect.Response[agentprivatev1.GetWorkReportResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	var end time.Time
	if value := request.Msg.End; value != nil {
		if err := value.CheckValid(); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		end = value.AsTime()
	}
	report, err := h.application.Queries.GetWorkReport.Handle(ctx, query.GetWorkReport{Scope: query.WorkReportScope{ProjectID: strings.TrimSpace(request.Msg.GetProjectId()), Purpose: strings.TrimSpace(request.Msg.GetPurpose())}, Window: request.Msg.GetWindow(), End: end})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.GetWorkReportResponse{Start: timestamppb.New(report.Start), End: timestamppb.New(report.End), ObservedAt: timestamppb.New(report.ObservedAt), StepSeconds: report.StepSeconds, Current: workPeriodToProto(report.Current), Previous: workPeriodToProto(report.Previous)}), nil
}

func workPeriodToProto(value query.WorkPeriodReport) *agentpublicv1.WorkPeriodReport {
	result := &agentpublicv1.WorkPeriodReport{Created: value.Created, Active: value.Active, Waiting: value.Waiting, Closed: value.Closed, HistoryComplete: value.HistoryComplete, EligibleWorkIds: value.EligibleWorkIDs}
	for _, bucket := range value.Buckets {
		result.Buckets = append(result.Buckets, &agentpublicv1.WorkReportBucket{Start: timestamppb.New(bucket.Start), Created: bucket.Created, Closed: bucket.Closed, P50Minutes: bucket.P50, P75Minutes: bucket.P75, P90Minutes: bucket.P90})
	}
	for _, group := range value.States {
		result.States = append(result.States, &agentpublicv1.WorkDistribution{Key: group.Key, Count: group.Count})
	}
	for _, group := range value.Owners {
		result.Owners = append(result.Owners, &agentpublicv1.WorkDistribution{Key: group.Key, Count: group.Count})
	}
	return result
}
