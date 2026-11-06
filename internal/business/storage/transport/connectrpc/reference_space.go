package connectrpc

import (
	"context"

	"connectrpc.com/connect"

	storageprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1/storageprivatev1connect"
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

func (h *ReferenceSpaceHandler) ReadReferenceFiles(ctx context.Context, request *connect.Request[storageprivatev1.ReadReferenceFilesRequest]) (*connect.Response[storageprivatev1.ReadReferenceFilesResponse], error) {
	if err := authenticateAny(request.Header().Get("Authorization"), h.tokens); err != nil {
		return nil, err
	}
	files, err := h.application.ReadReferenceFiles(ctx, request.Msg.GetOwner(), request.Msg.GetPaths())
	if err != nil {
		return nil, mapError(err)
	}
	response := &storageprivatev1.ReadReferenceFilesResponse{}
	for _, file := range files {
		response.Files = append(response.Files, &storageprivatev1.ReferenceFile{Path: string(file.Path), Content: file.Content})
	}
	return connect.NewResponse(response), nil
}

func (h *ReferenceSpaceHandler) ReplaceReferenceContent(ctx context.Context, request *connect.Request[storageprivatev1.ReplaceReferenceContentRequest]) (*connect.Response[storageprivatev1.ReplaceReferenceContentResponse], error) {
	if err := authenticateAny(request.Header().Get("Authorization"), h.tokens); err != nil {
		return nil, err
	}
	files := make([]application.ReferenceFile, 0, len(request.Msg.GetFiles()))
	for _, file := range request.Msg.GetFiles() {
		path, err := domain.ParseReferencePath(file.GetPath())
		if err != nil {
			return nil, mapError(err)
		}
		files = append(files, application.ReferenceFile{Path: path, Content: file.GetContent()})
	}
	if err := h.application.ReplaceReferenceContent(ctx, request.Msg.GetOwner(), files); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&storageprivatev1.ReplaceReferenceContentResponse{}), nil
}
