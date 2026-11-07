package storage

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/samber/oops"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
	"github.com/talgent-work/contracts/gen/go/storage/private/v1/storageprivatev1connect"
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

func NewWorkInputs(httpClient connecthttp.HTTPClient, config Config) (*WorkInputs, error) {
	if strings.TrimSpace(config.Address) == "" || strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("agent: Storage address and control token are required")
	}
	return &WorkInputs{client: storageprivatev1connect.NewFileSpaceServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, config.Address, connecthttp.WithReadMaxBytes(0)))), token: config.Token}, nil
}

func (w *WorkInputs) InputOwner(ctx context.Context, source string) (string, error) {
	response, err := w.client.ResolveInputSources(authorized(ctx, w.token), &storageprivatev1.ResolveInputSourcesRequest{InputSources: []string{source}})
	if err != nil {
		return "", oops.With("operation", "storage.resolve_input_sources").Wrap(err)
	}
	resolved := response.GetInputSources()
	if len(resolved) != 1 {
		return "", oops.With("operation", "storage.resolve_input_sources").Errorf("storage answered %d of 1 input sources", len(resolved))
	}
	return resolved[0].GetOwner(), nil
}

func (w *WorkInputs) InputAttachments(ctx context.Context, owner string) ([]execution.InputAttachment, error) {
	response, err := w.client.ListFiles(authorized(ctx, w.token), &storageprivatev1.ListFilesRequest{Owner: owner})
	if err != nil {
		return nil, oops.With("operation", "storage.list_files").Wrap(err)
	}
	var attachments []execution.InputAttachment
	for _, file := range response.GetFiles() {
		if file.GetKind() != storageprivatev1.FileKind_FILE_KIND_ATTACHMENT {
			continue
		}
		link, err := w.client.IssueAccessLink(authorized(ctx, w.token), &storageprivatev1.IssueAccessLinkRequest{
			Owner: owner, Kind: storageprivatev1.FileKind_FILE_KIND_ATTACHMENT, Name: file.GetName(), Disposition: storageprivatev1.AccessDisposition_ACCESS_DISPOSITION_DOWNLOAD,
		})
		if connect.CodeOf(err) == connect.CodeNotFound {
			// Deleted since listed; its change fact brings the next sync.
			continue
		}
		if err != nil {
			return nil, oops.With("operation", "storage.access").Wrap(err)
		}
		attachments = append(attachments, execution.InputAttachment{
			Name: file.GetName(), SizeBytes: file.GetSizeBytes(), UpdatedAt: file.GetUpdatedAt().AsTime(), AccessPath: link.GetAccessPath(),
		})
	}
	return attachments, nil
}
