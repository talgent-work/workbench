package workspacereading

import (
	"context"
	"io"
	"net/url"
	pathpkg "path"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

// machine is a Managed Work's directory as its machine serves it.
type machine struct {
	unreachable, unknown bool
	dirs                 map[string][]execution.WorkFile
	files                map[string]string
	listed               []string
}

func (m *machine) List(_ context.Context, _ domain.WorkspaceLocation, dir string) ([]execution.WorkFile, error) {
	if m.unreachable {
		return nil, domain.ErrWorkspaceUnavailable
	}
	m.listed = append(m.listed, dir)
	entries, ok := m.dirs[dir]
	switch {
	case m.unknown || (!ok && m.files[dir] == ""):
		return nil, execution.ErrWorkFileNotFound
	case !ok:
		return nil, execution.ErrWorkFileNotRegular
	}
	return entries, nil
}

func (m *machine) Stat(_ context.Context, _ domain.WorkspaceLocation, path string) (execution.WorkFile, error) {
	if m.unreachable {
		return execution.WorkFile{}, domain.ErrWorkspaceUnavailable
	}
	dir, name := pathpkg.Split(path)
	for _, entry := range m.dirs[strings.TrimSuffix(dir, "/")] {
		if entry.Name == name {
			return entry, nil
		}
	}
	return execution.WorkFile{}, execution.ErrWorkFileNotFound
}

func (m *machine) Open(_ context.Context, _ domain.WorkspaceLocation, path string) (io.ReadCloser, error) {
	if m.unreachable {
		return nil, domain.ErrWorkspaceUnavailable
	}
	content, ok := m.files[path]
	if !ok {
		return nil, execution.ErrWorkFileNotFound
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

// judgedFile is what Agent handed Storage's preview judgement.
type judgedFile struct {
	Name string
	Size int64
	Head int
}

// storage stands for Storage: its Workspace View of Cloud Works, and its
// Preview Policy, under which a .log is truncated to shownLog bytes, a .bin
// is refused, and anything else is shown whole.
type storage struct {
	judged []judgedFile
	reads  []string
}

const shownLog, wholeLimit = 100, 1000

func (s *storage) WorkspaceView(_ context.Context, location domain.WorkspaceLocation, _ string) (application.WorkspaceListing, error) {
	s.reads = append(s.reads, location.WorkID)
	return application.WorkspaceListing{}, nil
}

func (s *storage) PreviewWorkspaceFile(context.Context, domain.WorkspaceLocation, string) (application.WorkspaceFilePreview, error) {
	return application.WorkspaceFilePreview{}, nil
}

func (s *storage) WorkspaceFileLink(context.Context, domain.WorkspaceLocation, string) (application.WorkspaceFileLink, error) {
	return application.WorkspaceFileLink{}, nil
}

func (s *storage) JudgeFilePreview(_ context.Context, name string, size int64, head []byte) (application.WorkspaceFilePreview, int64, error) {
	s.judged = append(s.judged, judgedFile{Name: name, Size: size, Head: len(head)})
	switch {
	case strings.HasSuffix(name, ".log"):
		return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewTruncated, Display: application.WorkspacePreviewText, LimitBytes: shownLog}, shownLog, nil
	case strings.HasSuffix(name, ".bin"):
		return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewRefused, Refusal: application.WorkspacePreviewUnsupported}, 0, nil
	}
	return application.WorkspaceFilePreview{Mode: application.WorkspacePreviewDirect, Display: application.WorkspacePreviewText}, wholeLimit, nil
}

// sessionDirectory is where the Managed Work "session" has its files.
var sessionDirectory = domain.WorkspaceLocation{WorkID: "session", ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed"}

func newWorkspaces(t *testing.T) (*machine, *storage, *Workspaces, *application.WorkspaceFileSigner) {
	t.Helper()
	modified := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	big := strings.Repeat("a", previewHeadBytes+10)
	files := &machine{
		dirs: map[string][]execution.WorkFile{
			"":       {{Name: "inputs", Directory: true, ModifiedAt: modified}, {Name: "notes.md", Size: 5, ModifiedAt: modified}, {Name: "out", Directory: true, ModifiedAt: modified}},
			"inputs": {{Name: "brief.pdf", Size: 9, ModifiedAt: modified}},
			"out":    {{Name: "big.log", Size: int64(len(big)), ModifiedAt: modified}, {Name: "data.bin", Size: 3, ModifiedAt: modified}},
		},
		files: map[string]string{"notes.md": "# hi\n", "inputs/brief.pdf": "%PDF-1.7\n", "out/big.log": big, "out/data.bin": "\x00\x01\x02"},
	}
	links, err := application.NewWorkspaceFileSigner("/api/v1/agent/workspace-files", "control", time.Now)
	require.NoError(t, err)
	views := &storage{}
	workspaces, err := NewWorkspaces(views, files, links)
	require.NoError(t, err)
	return files, views, workspaces, links
}

// A Managed Work's directory is browsed on its machine: Attachments under
// inputs/ name themselves, a missing path is not found, a machine not
// reached is a Disconnection, and a machine that does not know the Work
// never opened its directory.
func TestManagedWorkspaceIsBrowsedOnTheMachine(t *testing.T) {
	ctx := context.Background()
	files, _, workspaces, _ := newWorkspaces(t)
	listing, err := workspaces.WorkspaceView(ctx, sessionDirectory, "")
	require.NoError(t, err)
	require.Equal(t, 3, listing.Total)
	require.Equal(t, application.WorkspaceDirectory, listing.Entries[0].Kind)
	require.Equal(t, application.WorkspaceFile, listing.Entries[1].Kind)
	require.Empty(t, listing.Entries[1].AttachmentName)
	inputs, err := workspaces.WorkspaceView(ctx, sessionDirectory, "inputs")
	require.NoError(t, err)
	require.Equal(t, "brief.pdf", inputs.Entries[0].AttachmentName, "an input is read as its Attachment")

	_, err = workspaces.WorkspaceView(ctx, sessionDirectory, "missing")
	require.ErrorIs(t, err, application.ErrWorkspacePathNotFound)
	_, err = workspaces.WorkspaceView(ctx, sessionDirectory, "../etc")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath)
	_, err = workspaces.WorkspaceView(ctx, sessionDirectory, "notes.md")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath)

	files.unknown = true
	_, err = workspaces.WorkspaceView(ctx, sessionDirectory, "")
	require.ErrorIs(t, err, domain.ErrWorkspaceNotCreated)
	files.unknown, files.unreachable = false, true
	_, err = workspaces.WorkspaceView(ctx, sessionDirectory, "")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	_, err = workspaces.PreviewWorkspaceFile(ctx, sessionDirectory, "notes.md")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	_, err = workspaces.WorkspaceFileLink(ctx, sessionDirectory, "notes.md")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
}

// Storage judges each preview from the file's name, size and at most its
// text probe of leading bytes; a readable file is linked for at most the
// bytes Storage allows, and a link opens the file it grants.
func TestManagedWorkspacePreviewsAreJudgedByStorage(t *testing.T) {
	ctx := context.Background()
	files, views, workspaces, links := newWorkspaces(t)
	preview, err := workspaces.PreviewWorkspaceFile(ctx, sessionDirectory, "notes.md")
	require.NoError(t, err)
	require.Equal(t, application.WorkspacePreviewDirect, preview.Mode)
	require.Equal(t, application.WorkspacePreviewText, preview.Display)
	require.NotNil(t, preview.Link)
	grant := linkGrant(t, links, preview.Link.URL)
	require.Equal(t, application.WorkspaceFileGrant{WorkID: "session", Path: "notes.md", Limit: wholeLimit, ExpiresAt: grant.ExpiresAt}, grant)

	preview, err = workspaces.PreviewWorkspaceFile(ctx, sessionDirectory, "out/big.log")
	require.NoError(t, err)
	require.Equal(t, application.WorkspacePreviewTruncated, preview.Mode)
	require.Equal(t, int64(shownLog), preview.LimitBytes)
	require.Equal(t, int64(shownLog), linkGrant(t, links, preview.Link.URL).Limit)

	preview, err = workspaces.PreviewWorkspaceFile(ctx, sessionDirectory, "out/data.bin")
	require.NoError(t, err)
	require.Equal(t, application.WorkspaceFilePreview{Mode: application.WorkspacePreviewRefused, Refusal: application.WorkspacePreviewUnsupported}, preview)
	require.Equal(t, []judgedFile{
		{Name: "notes.md", Size: 5, Head: 5}, {Name: "big.log", Size: previewHeadBytes + 10, Head: previewHeadBytes}, {Name: "data.bin", Size: 3, Head: 3},
	}, views.judged)

	_, err = workspaces.PreviewWorkspaceFile(ctx, sessionDirectory, "inputs/brief.pdf")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath, "an Attachment is previewed as itself")
	_, err = workspaces.PreviewWorkspaceFile(ctx, sessionDirectory, "out")
	require.ErrorIs(t, err, application.ErrInvalidWorkspacePath)
	_, err = workspaces.PreviewWorkspaceFile(ctx, sessionDirectory, "gone.txt")
	require.ErrorIs(t, err, application.ErrWorkspacePathNotFound)
	require.Empty(t, files.listed, "a file is described on its own, never by listing its directory")

	link, err := workspaces.WorkspaceFileLink(ctx, sessionDirectory, "out/data.bin")
	require.NoError(t, err)
	download := linkGrant(t, links, link.URL)
	require.True(t, download.Download)
	require.Zero(t, download.Limit, "a download is the whole file")
	require.WithinDuration(t, time.Now().Add(15*time.Minute), link.ExpiresAt, 2*time.Second)
	opened, err := workspaces.OpenWorkspaceFile(ctx, sessionDirectory, download)
	require.NoError(t, err)
	content, err := io.ReadAll(opened.Content)
	require.NoError(t, err)
	require.Equal(t, "\x00\x01\x02", string(content))
	require.Equal(t, "data.bin", opened.Name)
	require.True(t, opened.Download)
}

// A Cloud location is Storage's to read, and a link never opens a Cloud
// Work's file; a deployment that reaches no Managed Computer cannot read a
// Managed Work.
func TestOnlyAManagedWorkIsReadOnItsMachine(t *testing.T) {
	ctx := context.Background()
	files, views, workspaces, _ := newWorkspaces(t)
	cloudWork := domain.WorkspaceLocation{WorkID: uuid.NewString()}
	_, err := workspaces.WorkspaceView(ctx, cloudWork, "")
	require.NoError(t, err)
	require.Equal(t, []string{cloudWork.WorkID}, views.reads)
	require.Empty(t, files.listed)
	_, err = workspaces.OpenWorkspaceFile(ctx, cloudWork, application.WorkspaceFileGrant{WorkID: cloudWork.WorkID, Path: "notes.md"})
	require.ErrorIs(t, err, application.ErrInvalidWorkspaceFileLink)

	cloudOnly, err := NewWorkspaces(views, nil, nil)
	require.NoError(t, err)
	_, err = cloudOnly.WorkspaceView(ctx, sessionDirectory, "")
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
	_, err = cloudOnly.OpenWorkspaceFile(ctx, sessionDirectory, application.WorkspaceFileGrant{WorkID: "session", Path: "notes.md"})
	require.ErrorIs(t, err, domain.ErrWorkspaceUnavailable)
}

func linkGrant(t *testing.T, links *application.WorkspaceFileSigner, link string) application.WorkspaceFileGrant {
	t.Helper()
	parsed, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, "/api/v1/agent/workspace-files", parsed.Path)
	grant, err := links.Verify(parsed.Query().Get("grant"))
	require.NoError(t, err)
	return grant
}
