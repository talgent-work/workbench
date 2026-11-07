package connectrpc

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"google.golang.org/protobuf/proto"
)

// errorDetail serializes a registered message, which always succeeds.
func errorDetail(message proto.Message) *connect.ErrorDetail {
	detail, err := connectproto.NewErrorDetail(message)
	if err != nil {
		panic(err)
	}
	return detail
}

var (
	// sandboxUnavailable details a start refused because its Computer cannot
	// provide Claude Code's sandbox.
	sandboxUnavailable = errorDetail(&agentpublicv1.SandboxUnavailable{})
	// computerOutdated details a start refused because its Managed Computer
	// runs a Talgent Computer below the Minimum Version.
	computerOutdated = errorDetail(&agentpublicv1.ComputerOutdated{})
)

// withRefusalDetail details a Computer's refusal its owner acts on, by which
// Start Work tells it apart rather than by its text.
func withRefusalDetail(refused *connect.Error, err error) *connect.Error {
	switch {
	case errors.Is(err, domain.ErrComputerNoSandbox):
		refused = refused.WithDetail(sandboxUnavailable)
	case errors.Is(err, domain.ErrComputerOutdated):
		refused = refused.WithDetail(computerOutdated)
	}
	return refused
}

var permissionModesToProto = map[domain.PermissionMode]agentpublicv1.PermissionMode{
	domain.PermissionAsk:         agentpublicv1.PermissionMode_PERMISSION_MODE_ASK,
	domain.PermissionAcceptEdits: agentpublicv1.PermissionMode_PERMISSION_MODE_ACCEPT_EDITS,
	domain.PermissionPlan:        agentpublicv1.PermissionMode_PERMISSION_MODE_PLAN,
	domain.PermissionBypass:      agentpublicv1.PermissionMode_PERMISSION_MODE_BYPASS_PERMISSIONS,
	domain.PermissionDontAsk:     agentpublicv1.PermissionMode_PERMISSION_MODE_DONT_ASK,
	domain.PermissionAuto:        agentpublicv1.PermissionMode_PERMISSION_MODE_AUTO,
}

// permissionModeToProto is unspecified for a Cloud Work, which has none.
func permissionModeToProto(mode domain.PermissionMode) agentpublicv1.PermissionMode {
	return permissionModesToProto[mode]
}

// permissionModeFromProto maps a chosen mode; an unknown value maps to one
// the Work refuses.
func permissionModeFromProto(mode agentpublicv1.PermissionMode) domain.PermissionMode {
	if mode == agentpublicv1.PermissionMode_PERMISSION_MODE_UNSPECIFIED {
		return ""
	}
	for domainMode, protoMode := range permissionModesToProto {
		if protoMode == mode {
			return domainMode
		}
	}
	return domain.PermissionMode(mode.String())
}

func harnessChoiceFromProto(choice *agentpublicv1.HarnessChoice) domain.HarnessChoice {
	return domain.HarnessChoice{PermissionMode: permissionModeFromProto(choice.GetPermissionMode()), Sandbox: choice.GetSandbox()}
}

func (h *WorkHandler) SetPermissionMode(ctx context.Context, request *agentpublicv1.SetPermissionModeRequest) (*agentpublicv1.SetPermissionModeResponse, error) {
	actor, _ := authn.CurrentUserID(ctx)
	mode := permissionModeFromProto(request.GetPermissionMode())
	if mode == "" {
		return nil, mapAgentError(domain.ErrInvalidWork)
	}
	accepted, err := h.application.SetPermissionMode(ctx, application.SetPermissionMode{ActorUserID: actor, WorkID: strings.TrimSpace(request.GetWorkId()), Mode: mode})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.SetPermissionModeResponse{PermissionMode: permissionModeToProto(accepted)}, nil
}
