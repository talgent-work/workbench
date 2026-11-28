package connectrpc

import (
	"context"
	"errors"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// PersonalVariableHandler serves a signed-in person's own Personal
// Variables. The person is the user principal the entry established, never
// a name in the request, so nobody reads or changes another's.
type PersonalVariableHandler struct {
	application *application.Application
}

var _ agentpublicv1connect.PersonalVariableServiceHandler = (*PersonalVariableHandler)(nil)

func NewPersonalVariableHandler(app *application.Application) agentpublicv1connect.PersonalVariableServiceHandler {
	return &PersonalVariableHandler{application: app}
}

func (h *PersonalVariableHandler) ListPersonalVariables(ctx context.Context, _ *agentpublicv1.ListPersonalVariablesRequest) (*agentpublicv1.ListPersonalVariablesResponse, error) {
	person, ok := authn.CurrentUserID(ctx)
	if !ok || person == "" {
		return nil, errNoPerson
	}
	names, err := h.application.Queries.PersonalVariables.Names(ctx, person)
	if err != nil {
		return nil, mapPersonalVariableError(err)
	}
	response := &agentpublicv1.ListPersonalVariablesResponse{}
	for _, name := range names {
		response.Variables = append(response.Variables, &agentpublicv1.PersonalVariableSummary{Name: name})
	}
	return response, nil
}

func (h *PersonalVariableHandler) RevealPersonalVariable(ctx context.Context, request *agentpublicv1.RevealPersonalVariableRequest) (*agentpublicv1.RevealPersonalVariableResponse, error) {
	person, ok := authn.CurrentUserID(ctx)
	if !ok || person == "" {
		return nil, errNoPerson
	}
	value, err := h.application.Queries.PersonalVariables.Reveal(ctx, person, strings.TrimSpace(request.GetName()))
	if err != nil {
		return nil, mapPersonalVariableError(err)
	}
	return &agentpublicv1.RevealPersonalVariableResponse{Value: value}, nil
}

func (h *PersonalVariableHandler) SetPersonalVariable(ctx context.Context, request *agentpublicv1.SetPersonalVariableRequest) (*agentpublicv1.SetPersonalVariableResponse, error) {
	person, ok := authn.CurrentUserID(ctx)
	if !ok || person == "" {
		return nil, errNoPerson
	}
	variable := domain.PersonalVariable{Name: strings.TrimSpace(request.GetName()), Value: request.GetValue()}
	if err := h.application.Commands.PersonalVariables.Set(ctx, command.SetPersonalVariable{Requester: person, Variable: variable}); err != nil {
		return nil, mapPersonalVariableError(err)
	}
	return &agentpublicv1.SetPersonalVariableResponse{Variable: &agentpublicv1.PersonalVariableSummary{Name: variable.Name}}, nil
}

func (h *PersonalVariableHandler) DeletePersonalVariable(ctx context.Context, request *agentpublicv1.DeletePersonalVariableRequest) (*agentpublicv1.DeletePersonalVariableResponse, error) {
	person, ok := authn.CurrentUserID(ctx)
	if !ok || person == "" {
		return nil, errNoPerson
	}
	if err := h.application.Commands.PersonalVariables.Remove(ctx, command.RemovePersonalVariable{Requester: person, Name: strings.TrimSpace(request.GetName())}); err != nil {
		return nil, mapPersonalVariableError(err)
	}
	return &agentpublicv1.DeletePersonalVariableResponse{}, nil
}

// mapPersonalVariableError names the broken rule of a refusal, whose message
// never carries a value; any other failure answers a fixed message and keeps
// its cause for the request log alone.
func mapPersonalVariableError(err error) error {
	var code connect.Code
	switch {
	case errors.Is(err, domain.ErrInvalidPersonalVariable):
		code = connect.CodeInvalidArgument
	case errors.Is(err, domain.ErrPersonalVariableNotFound):
		code = connect.CodeNotFound
	case errors.Is(err, domain.ErrPersonalVariableForbidden):
		code = connect.CodePermissionDenied
	case errors.Is(err, domain.ErrConcurrentModification):
		code = connect.CodeAborted
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	default:
		return connect.NewError(connect.CodeInternal, "agent: Personal Variables are unavailable").WithCause(err)
	}
	return connect.NewError(code, err.Error()).WithCause(err)
}

// personalVariableChoiceFromProto maps a start's Personal Variable selection:
// an absent one enables every Personal Variable of the person, a present one
// only the names it lists, none when it lists none.
func personalVariableChoiceFromProto(selection *agentpublicv1.PersonalVariableSelection) domain.PersonalVariableChoice {
	if selection == nil {
		return domain.PersonalVariableChoice{}
	}
	return domain.PersonalVariableChoice{Chosen: true, Names: slices.Clone(selection.GetNames())}
}
