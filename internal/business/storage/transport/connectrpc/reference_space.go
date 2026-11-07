package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"

	storageprivatev1 "github.com/talgent-work/contracts/gen/storage/private/v1"
	"github.com/talgent-work/contracts/gen/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// ReferenceSpaceHandler serves the Reference Space Contract to the contexts
// that maintain Reference Spaces, each authenticated by its control token.
type ReferenceSpaceHandler struct {
	application *application.Application
	tokens      []string
}

var _ storageprivatev1connect.ReferenceSpaceServiceHandler = (*ReferenceSpaceHandler)(nil)

func NewReferenceSpaceHandler(app *application.Application, tokens ...string) *ReferenceSpaceHandler {
	return &ReferenceSpaceHandler{application: app, tokens: tokens}
}

func (h *ReferenceSpaceHandler) ReadReferenceFiles(ctx context.Context, request *storageprivatev1.ReadReferenceFilesRequest) (*storageprivatev1.ReadReferenceFilesResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.tokens); err != nil {
		return nil, err
	}
	files, err := h.application.ReadReferenceFiles(ctx, request.GetOwner(), request.GetPaths())
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ReadReferenceFilesResponse{}
	for _, file := range files {
		response.Files = append(response.Files, &storageprivatev1.ReferenceFile{Path: string(file.Path), Content: file.Content})
	}
	return response, nil
}

func (h *ReferenceSpaceHandler) ReplaceReferenceContent(ctx context.Context, request *storageprivatev1.ReplaceReferenceContentRequest) (*storageprivatev1.ReplaceReferenceContentResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if err := authenticateAny(info.RequestHeader().Get("Authorization"), h.tokens); err != nil {
		return nil, err
	}
	files := make([]application.ReferenceFile, 0, len(request.GetFiles()))
	for _, file := range request.GetFiles() {
		path, err := domain.ParseReferencePath(file.GetPath())
		if err != nil {
			return nil, mapError(err)
		}
		files = append(files, application.ReferenceFile{Path: path, Content: file.GetContent()})
	}
	if err := h.application.ReplaceReferenceContent(ctx, request.GetOwner(), files); err != nil {
		return nil, mapError(err)
	}
	return &storageprivatev1.ReplaceReferenceContentResponse{}, nil
}
