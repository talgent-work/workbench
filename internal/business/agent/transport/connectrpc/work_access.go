package connectrpc

import (
	"context"
	"strings"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
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
	domain.ReadAsOwner:            agentprivatev1.WorkReadBasis_WORK_READ_BASIS_OWNER,
	domain.ReadAsShare:            agentprivatev1.WorkReadBasis_WORK_READ_BASIS_SHARE,
	domain.ReadAsPublic:           agentprivatev1.WorkReadBasis_WORK_READ_BASIS_PUBLIC,
	domain.ReadAsApplicationOwner: agentprivatev1.WorkReadBasis_WORK_READ_BASIS_APPLICATION_OWNER,
}

func (h *WorkAccessHandler) AdmitWorkReader(ctx context.Context, request *agentprivatev1.AdmitWorkReaderRequest) (*agentprivatev1.AdmitWorkReaderResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	admitted, err := h.application.AdmitWorkReader(ctx, application.AdmitWorkReader{ReaderID: strings.TrimSpace(request.GetReaderId()), WorkID: strings.TrimSpace(request.GetWorkId()), Admission: readerAdmissionFromProto(request.GetAdmission()), IncludeWorkState: request.GetIncludeWorkState()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.AdmitWorkReaderResponse{Basis: readBases[admitted.Basis]}
	if admitted.Work != nil {
		response.WorkState = WorkStateFrame(*admitted.Work)
	}
	return response, nil
}

func (h *WorkAccessHandler) GrantWorkShare(ctx context.Context, request *agentprivatev1.GrantWorkShareRequest) (*agentprivatev1.GrantWorkShareResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	err := h.application.GrantWorkShare(ctx, application.GrantWorkShare{RequesterID: strings.TrimSpace(request.GetRequesterId()), WorkID: strings.TrimSpace(request.GetWorkId()), ReaderID: strings.TrimSpace(request.GetReaderId()), Admission: readerAdmissionFromProto(request.GetAdmission())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.GrantWorkShareResponse{}, nil
}

func (h *WorkAccessHandler) RevokeDepartedReader(ctx context.Context, request *agentprivatev1.RevokeDepartedReaderRequest) (*agentprivatev1.RevokeDepartedReaderResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	err := h.application.RevokeDepartedReader(ctx, application.RevokeDepartedReader{ReaderID: strings.TrimSpace(request.GetReaderId()), Scope: readerAdmissionFromProto(request.GetDeparted())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.RevokeDepartedReaderResponse{}, nil
}

func readerAdmissionFromProto(value *agentprivatev1.ReaderAdmission) domain.ReaderAdmission {
	// Only a Work Caller of an extension-defined kind admits readers; any
	// other admits nothing.
	caller, err := domain.NewWorkCaller(value.GetWorkCaller().GetKind(), value.GetWorkCaller().GetId())
	if err != nil || !caller.ExtensionDefined() {
		return domain.ReaderAdmission{}
	}
	return domain.ReaderAdmission{WorkCaller: caller}
}
