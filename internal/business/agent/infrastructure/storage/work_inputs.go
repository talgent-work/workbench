package storage

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/samber/oops"

	storageprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/storage/private/v1/storageprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

// WorkInputs reads a Managed Work's input source through Storage's File Space
// Contract: the File Space owner it names, and that space's Attachments, each
// with a fresh download Access Link.
type WorkInputs struct {
	client storageprivatev1connect.FileSpaceServiceClient
	token  string
}

var _ execution.WorkInputFiles = (*WorkInputs)(nil)

func NewWorkInputs(httpClient connect.HTTPClient, config Config) (*WorkInputs, error) {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("agent: Storage address and control token are required")
	}
	return &WorkInputs{client: storageprivatev1connect.NewFileSpaceServiceClient(httpClient, config.Address), token: config.Token}, nil
}

func (w *WorkInputs) InputOwner(ctx context.Context, source string) (string, error) {
	response, err := w.client.ResolveInputSources(ctx, request(&storageprivatev1.ResolveInputSourcesRequest{InputSources: []string{source}}, w.token))
	if err != nil {
		return "", oops.With("operation", "storage.resolve_input_sources").Wrap(err)
	}
	resolved := response.Msg.GetInputSources()
	if len(resolved) != 1 {
		return "", oops.With("operation", "storage.resolve_input_sources").Errorf("storage answered %d of 1 input sources", len(resolved))
	}
	return resolved[0].GetOwner(), nil
}

func (w *WorkInputs) InputAttachments(ctx context.Context, owner string) ([]execution.InputAttachment, error) {
	response, err := w.client.ListFiles(ctx, request(&storageprivatev1.ListFilesRequest{Owner: owner}, w.token))
	if err != nil {
		return nil, oops.With("operation", "storage.list_files").Wrap(err)
	}
	var attachments []execution.InputAttachment
	for _, file := range response.Msg.GetFiles() {
		if file.GetKind() != storageprivatev1.FileKind_FILE_KIND_ATTACHMENT {
			continue
		}
		link, err := w.client.IssueAccessLink(ctx, request(&storageprivatev1.IssueAccessLinkRequest{
			Owner: owner, Kind: storageprivatev1.FileKind_FILE_KIND_ATTACHMENT, Name: file.GetName(), Disposition: storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_DOWNLOAD,
		}, w.token))
		if connect.CodeOf(err) == connect.CodeNotFound {
			// Deleted since listed; its change fact brings the next sync.
			continue
		}
		if err != nil {
			return nil, oops.With("operation", "storage.access").Wrap(err)
		}
		attachments = append(attachments, execution.InputAttachment{
			Name: file.GetName(), SizeBytes: file.GetSizeBytes(), UpdatedAt: file.GetUpdatedAt().AsTime(), AccessPath: link.Msg.GetAccessPath(),
		})
	}
	return attachments, nil
}
