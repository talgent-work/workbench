package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Managed Work's file is read from its machine, so its links are Agent's
// own, as Storage's Access Links are Storage's: each grants reads of one path
// of one Work, as often as it is opened, until it expires, and needs no
// signed-in session. The reader was admitted when the link was issued; it is
// refused again once the Work is Closed.
//
// The signing key is derived from the Computer control token
// (agent-computer.token), which only Agent and Computer's services hold.
// Rotating that token invalidates every outstanding link.

// workspaceFileLinkTTL bounds a link as Storage's Access Links are bounded
// (storage.link-ttl).
const workspaceFileLinkTTL = 15 * time.Minute

// WorkspaceFileGrant is what one of Agent's links grants to read.
type WorkspaceFileGrant struct {
	WorkID string `json:"w"`
	Path   string `json:"p"`
	// Download serves the file under its name as an attachment; otherwise it
	// is shown inline.
	Download bool `json:"d,omitempty"`
	// Limit bounds the bytes served, zero for the whole file.
	Limit     int64 `json:"l,omitempty"`
	ExpiresAt int64 `json:"e"`
}

// WorkspaceFileSigner issues and verifies Agent's own links to Works' files.
type WorkspaceFileSigner struct {
	address string
	key     []byte
	now     func() time.Time
}

// workspaceFileLinkKeyLabel separates the link key from the control token it
// is derived from, so a link's signature tells nothing of the token.
const workspaceFileLinkKeyLabel = "talgent agent workspace file link v1"

// NewWorkspaceFileSigner issues links at address, where the platform routes
// Agent's workspace file endpoint (a path such as
// /api/v1/agent/workspace-files on the reader's origin), signed with a key
// derived from secret.
func NewWorkspaceFileSigner(address, secret string, now func() time.Time) (*WorkspaceFileSigner, error) {
	parsed, err := url.Parse(address)
	if err != nil || strings.Trim(parsed.Path, "/") == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || strings.TrimSpace(secret) == "" || now == nil {
		return nil, errors.New("agent: a workspace file link address and secret are required for Managed Computers")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(workspaceFileLinkKeyLabel))
	return &WorkspaceFileSigner{address: strings.TrimRight(address, "/"), key: mac.Sum(nil), now: now}, nil
}

// Issue signs a grant that expires after the links' lifetime.
func (l *WorkspaceFileSigner) Issue(grant WorkspaceFileGrant) WorkspaceFileLink {
	expiresAt := l.now().UTC().Add(workspaceFileLinkTTL).Truncate(time.Second)
	grant.ExpiresAt = expiresAt.Unix()
	payload, _ := json.Marshal(grant)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return WorkspaceFileLink{URL: l.address + "?" + url.Values{"grant": {encoded + "." + l.signature(encoded)}}.Encode(), ExpiresAt: expiresAt}
}

// Verify returns the grant a link's token carries; ErrInvalidWorkspaceFileLink
// for an unsigned, altered or expired one.
func (l *WorkspaceFileSigner) Verify(token string) (WorkspaceFileGrant, error) {
	encoded, signature, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || encoded == "" || !hmac.Equal([]byte(signature), []byte(l.signature(encoded))) {
		return WorkspaceFileGrant{}, ErrInvalidWorkspaceFileLink
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return WorkspaceFileGrant{}, ErrInvalidWorkspaceFileLink
	}
	var grant WorkspaceFileGrant
	if err := json.Unmarshal(payload, &grant); err != nil || grant.WorkID == "" || grant.Path == "" || grant.Limit < 0 || grant.ExpiresAt <= l.now().Unix() {
		return WorkspaceFileGrant{}, ErrInvalidWorkspaceFileLink
	}
	return grant, nil
}

func (l *WorkspaceFileSigner) signature(encoded string) string {
	mac := hmac.New(sha256.New, l.key)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// WorkspaceFileLinks opens Agent's own links to Works' files. A link admits
// its reader by itself, until the Work is Closed.
type WorkspaceFileLinks struct {
	works  domain.AgentWorkRepository
	files  WorkspacePort
	signer *WorkspaceFileSigner
}

func NewWorkspaceFileLinks(works domain.AgentWorkRepository, files WorkspacePort, signer *WorkspaceFileSigner) (*WorkspaceFileLinks, error) {
	if works == nil || files == nil || signer == nil {
		return nil, errors.New("agent: Works, workspaces and a link signer are required for workspace file links")
	}
	return &WorkspaceFileLinks{works: works, files: files, signer: signer}, nil
}

// Open opens the file a link grants while its Work is not Closed: a Closed
// Work's workspace is not read, by a link issued earlier either. The file is
// read where the Work's AgentWork locates it.
func (l *WorkspaceFileLinks) Open(ctx context.Context, token string) (OpenedWorkspaceFile, error) {
	grant, err := l.signer.Verify(token)
	if err != nil {
		return OpenedWorkspaceFile{}, err
	}
	work, err := l.works.Get(ctx, grant.WorkID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return OpenedWorkspaceFile{}, ErrInvalidWorkspaceFileLink
	}
	if err != nil {
		return OpenedWorkspaceFile{}, err
	}
	if work.ClosureConfirmed() {
		return OpenedWorkspaceFile{}, ErrWorkspaceClosed
	}
	location, err := work.LocateWorkspace()
	if err != nil {
		return OpenedWorkspaceFile{}, err
	}
	return l.files.OpenWorkspaceFile(ctx, location, grant)
}

// OpenedWorkspaceFile is one file a link reads, streamed from where the Work's
// files are; the reader closes Content.
type OpenedWorkspaceFile struct {
	Name     string
	Download bool
	// Limit bounds the bytes served, zero for the whole file.
	Limit   int64
	Content io.ReadCloser
}
