package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
)

// SandboxMountHandler serves the Sandbox Mount, Workspace View and Work
// Reclamation contracts to Agent only.
type SandboxMountHandler struct {
	application *application.Application
	token       string
}

var _ storageprivatev1connect.SandboxMountServiceHandler = (*SandboxMountHandler)(nil)

func NewSandboxMountHandler(app *application.Application, token string) *SandboxMountHandler {
	return &SandboxMountHandler{application: app, token: token}
}

func (h *SandboxMountHandler) agent(ctx context.Context) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	return authenticate(info.RequestHeader().Get("Authorization"), h.token)
}

func (h *SandboxMountHandler) AcquireMounts(ctx context.Context, request *storageprivatev1.AcquireMountsRequest) (*storageprivatev1.AcquireMountsResponse, error) {
	if err := h.agent(ctx); err != nil {
		return nil, err
	}
	requests := make([]application.MountRequest, 0, len(request.GetMounts()))
	for _, mount := range request.GetMounts() {
		requests = append(requests, application.MountRequest{
			Directory: locatedFromProto(mount.GetDirectory()), ReadOnly: mount.GetReadOnly(), MountPoints: mount.GetMountPoints(),
		})
	}
	grants, err := h.application.AcquireMounts(ctx, request.GetWorkId(), requests)
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.AcquireMountsResponse{Grants: make([]*storageprivatev1.MountGrant, 0, len(grants))}
	for _, grant := range grants {
		response.Grants = append(response.Grants, mountGrantToProto(grant))
	}
	return response, nil
}

func (h *SandboxMountHandler) GetWorkspaceView(ctx context.Context, request *storageprivatev1.GetWorkspaceViewRequest) (*storageprivatev1.GetWorkspaceViewResponse, error) {
	if err := h.agent(ctx); err != nil {
		return nil, err
	}
	listing, err := h.application.WorkspaceView(ctx, layoutFromProto(request.GetLayout()), request.GetPath())
	if err != nil {
		return nil, mapError(err)
	}
	return workspaceListingToProto(listing), nil
}

func (h *SandboxMountHandler) GetWorkspaceFilePreview(ctx context.Context, request *storageprivatev1.GetWorkspaceFilePreviewRequest) (*storageprivatev1.GetWorkspaceFilePreviewResponse, error) {
	if err := h.agent(ctx); err != nil {
		return nil, err
	}
	preview, err := h.application.PreviewWorkspaceFile(ctx, layoutFromProto(request.GetLayout()), request.GetPath())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.GetWorkspaceFilePreviewResponse{Preview: filePreviewToProto(preview)}, nil
}

func (h *SandboxMountHandler) IssueWorkspaceFileLink(ctx context.Context, request *storageprivatev1.IssueWorkspaceFileLinkRequest) (*storageprivatev1.IssueWorkspaceFileLinkResponse, error) {
	if err := h.agent(ctx); err != nil {
		return nil, err
	}
	link, err := h.application.IssueWorkspaceFileLink(ctx, layoutFromProto(request.GetLayout()), request.GetPath())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.IssueWorkspaceFileLinkResponse{AccessUrl: link.URL, ExpiresAt: timestamppb.New(link.ExpiresAt)}, nil
}

func (h *SandboxMountHandler) RequestWorkReclamation(ctx context.Context, request *storageprivatev1.RequestWorkReclamationRequest) (*storageprivatev1.RequestWorkReclamationResponse, error) {
	if err := h.agent(ctx); err != nil {
		return nil, err
	}
	if err := h.application.RequestWorkReclamation(ctx, request.GetWorkId()); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.RequestWorkReclamationResponse{}, nil
}

func (h *SandboxMountHandler) JudgeFilePreview(ctx context.Context, request *storageprivatev1.JudgeFilePreviewRequest) (*storageprivatev1.JudgeFilePreviewResponse, error) {
	if err := h.agent(ctx); err != nil {
		return nil, err
	}
	judged, err := h.application.JudgeFilePreview(request.GetName(), request.GetSizeBytes(), request.GetHead())
	if err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.JudgeFilePreviewResponse{
		Preview: filePreviewToProto(application.FilePreview{Verdict: judged.Verdict}), ReadLimitBytes: judged.ReadLimit,
	}, nil
}
