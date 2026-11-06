package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkAccessHandler serves trusted callers deciding message-stream readers.
type WorkAccessHandler struct {
	application *application.Application
	token       string
}

var _ agentprivatev1connect.WorkAccessServiceHandler = (*WorkAccessHandler)(nil)

func NewWorkAccessHandler(app *application.Application, token string) *WorkAccessHandler {
	return &WorkAccessHandler{application: app, token: token}
}

var readBases = map[domain.ReadBasis]agentprivatev1.WorkReadBasis{
	domain.ReadAsOwner:  agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER,
	domain.ReadAsShare:  agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE,
	domain.ReadAsPublic: agentprivatev1.WorkReadBasis_WORK_READ_BASIS_PUBLIC,
}

func (h *WorkAccessHandler) AdmitWorkReader(ctx context.Context, request *connect.Request[agentprivatev1.AdmitWorkReaderRequest]) (*connect.Response[agentprivatev1.AdmitWorkReaderResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	admitted, err := h.application.AdmitWorkReader(ctx, application.AdmitWorkReader{ReaderID: strings.TrimSpace(request.Msg.GetReaderId()), WorkID: strings.TrimSpace(request.Msg.GetWorkId()), Admission: readerAdmissionFromProto(request.Msg.GetAdmission()), IncludeWorkState: request.Msg.GetIncludeWorkState()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.AdmitWorkReaderResponse{Basis: readBases[admitted.Basis]}
	if admitted.Work != nil {
		response.WorkState = WorkStateFrame(*admitted.Work)
	}
	return connect.NewResponse(response), nil
}

func (h *WorkAccessHandler) GrantWorkShare(ctx context.Context, request *connect.Request[agentprivatev1.GrantWorkShareRequest]) (*connect.Response[agentprivatev1.GrantWorkShareResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	err := h.application.GrantWorkShare(ctx, application.GrantWorkShare{RequesterID: strings.TrimSpace(request.Msg.GetRequesterId()), WorkID: strings.TrimSpace(request.Msg.GetWorkId()), ReaderID: strings.TrimSpace(request.Msg.GetReaderId()), Admission: readerAdmissionFromProto(request.Msg.GetAdmission())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.GrantWorkShareResponse{}), nil
}

func (h *WorkAccessHandler) RevokeDepartedReader(ctx context.Context, request *connect.Request[agentprivatev1.RevokeDepartedReaderRequest]) (*connect.Response[agentprivatev1.RevokeDepartedReaderResponse], error) {
	if err := authenticateIntegration(h.token, request.Header().Get("Authorization")); err != nil {
		return nil, err
	}
	err := h.application.RevokeDepartedReader(ctx, application.RevokeDepartedReader{ReaderID: strings.TrimSpace(request.Msg.GetReaderId()), Scope: readerAdmissionFromProto(request.Msg.GetScope())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentprivatev1.RevokeDepartedReaderResponse{}), nil
}

func readerAdmissionFromProto(value *agentprivatev1.ReaderAdmission) domain.ReaderAdmission {
	// Only a Project admits readers; any other reference admits nothing.
	if strings.TrimSpace(value.GetReference()) != "project" {
		return domain.ReaderAdmission{}
	}
	return domain.ReaderAdmission{ProjectID: strings.TrimSpace(value.GetValue())}
}
