package infrastructure

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
)

// WorkAccess asks Agent, from Agent's own facts, whether a reader may read a
// Work's message stream. It never calls the contexts that use Agent.
type WorkAccess struct {
	client agentprivatev1connect.WorkAccessServiceClient
	token  string
}

var _ query.WorkAccessPort = (*WorkAccess)(nil)

func NewWorkAccess(client connecthttp.HTTPClient, address, token string) (*WorkAccess, error) {
	if client == nil || strings.TrimSpace(address) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("streamgateway: Agent endpoint and control token are required")
	}
	return &WorkAccess{client: agentprivatev1connect.NewWorkAccessServiceClient(connect.NewClient(connecthttp.NewTransport(client, address, connecthttp.WithReadMaxBytes(0)))), token: token}, nil
}

func (a *WorkAccess) AuthorizeRead(ctx context.Context, reader string, admission query.ReaderAdmission, workID string) error {
	_, err := a.admit(ctx, reader, admission, workID, false)
	return err
}

func (a *WorkAccess) AdmitStreamReader(ctx context.Context, reader string, admission query.ReaderAdmission, workID string) (*query.WorkState, error) {
	response, err := a.admit(ctx, reader, admission, workID, true)
	if err != nil || response.GetWorkState() == nil {
		return nil, err
	}
	return workStateFromProto(response.GetWorkState()), nil
}

func (a *WorkAccess) admit(ctx context.Context, reader string, admission query.ReaderAdmission, workID string, withState bool) (*agentprivatev1.AdmitWorkReaderResponse, error) {
	if strings.TrimSpace(reader) == "" || strings.TrimSpace(workID) == "" {
		return nil, query.ErrWorkAccessDenied
	}
	request := &agentprivatev1.AdmitWorkReaderRequest{WorkId: workID, ReaderId: reader, Admission: &agentprivatev1.ReaderAdmission{Reference: admission.Reference, Value: admission.Value}, IncludeWorkState: withState}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+a.token)
	response, err := a.client.AdmitWorkReader(ctx, request)
	if err != nil {
		switch connect.CodeOf(err) {
		case connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeNotFound, connect.CodeInvalidArgument:
			return nil, query.ErrWorkAccessDenied
		default:
			return nil, errors.Join(query.ErrWorkAccessUnavailable, err)
		}
	}
	if response.GetBasis() == agentprivatev1.WorkReadBasis_WORK_READ_BASIS_UNSPECIFIED {
		return nil, query.ErrWorkAccessDenied
	}
	return response, nil
}
