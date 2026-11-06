package execution

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application"
)

// A Managed Work's file is read from its machine, so its links are Agent's
// own, as Storage's Access Links are Storage's: each grants reads of one path
// of one Work, as often as it is opened, until it expires, and needs no
// signed-in session. The reader was admitted when the link was issued; the
// application layer refuses it again once the Work is Closed.
//
// The signing key is derived from the Computer control token
// (agent-computer.token), which only Agent and Computer's services hold.
// Rotating that token invalidates every outstanding link.

// workspaceFileLinkTTL bounds a link as Storage's Access Links are bounded
// (storage.link-ttl).
const workspaceFileLinkTTL = 15 * time.Minute

// workspaceFileGrant is what a link grants to read.
type workspaceFileGrant struct {
	WorkID string `json:"w"`
	Path   string `json:"p"`
	// Download serves the file under its name as an attachment; otherwise it
	// is shown inline.
	Download bool `json:"d,omitempty"`
	// Limit bounds the bytes served, zero for the whole file.
	Limit     int64 `json:"l,omitempty"`
	ExpiresAt int64 `json:"e"`
}

// WorkspaceFileLinks signs and verifies the links to Managed Works' files.
type WorkspaceFileLinks struct {
	address string
	key     []byte
	now     func() time.Time
}

// workspaceFileLinkKeyLabel separates the link key from the control token it
// is derived from, so a link's signature tells nothing of the token.
const workspaceFileLinkKeyLabel = "talgent agent workspace file link v1"

// NewWorkspaceFileLinks serves links at address, where the platform routes
// Agent's workspace file endpoint (a path such as /api/v1/agent/workspace-files
// on the reader's origin), signed with a key derived from secret.
func NewWorkspaceFileLinks(address, secret string, now func() time.Time) (*WorkspaceFileLinks, error) {
	parsed, err := url.Parse(address)
	if err != nil || strings.Trim(parsed.Path, "/") == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || strings.TrimSpace(secret) == "" || now == nil {
		return nil, errors.New("agent: a workspace file link address and secret are required for Managed Computers")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(workspaceFileLinkKeyLabel))
	return &WorkspaceFileLinks{address: strings.TrimRight(address, "/"), key: mac.Sum(nil), now: now}, nil
}

// issue signs a grant that expires after the links' lifetime.
func (l *WorkspaceFileLinks) issue(grant workspaceFileGrant) (string, time.Time) {
	expiresAt := l.now().UTC().Add(workspaceFileLinkTTL).Truncate(time.Second)
	grant.ExpiresAt = expiresAt.Unix()
	payload, _ := json.Marshal(grant)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return l.address + "?" + url.Values{"grant": {encoded + "." + l.signature(encoded)}}.Encode(), expiresAt
}

// verify returns the grant a link's token carries.
func (l *WorkspaceFileLinks) verify(token string) (workspaceFileGrant, error) {
	encoded, signature, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || encoded == "" || !hmac.Equal([]byte(signature), []byte(l.signature(encoded))) {
		return workspaceFileGrant{}, application.ErrInvalidWorkspaceFileLink
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return workspaceFileGrant{}, application.ErrInvalidWorkspaceFileLink
	}
	var grant workspaceFileGrant
	if err := json.Unmarshal(payload, &grant); err != nil || grant.WorkID == "" || grant.Path == "" || grant.Limit < 0 || grant.ExpiresAt <= l.now().Unix() {
		return workspaceFileGrant{}, application.ErrInvalidWorkspaceFileLink
	}
	return grant, nil
}

func (l *WorkspaceFileLinks) signature(encoded string) string {
	mac := hmac.New(sha256.New, l.key)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
