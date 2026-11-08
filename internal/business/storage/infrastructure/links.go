package infrastructure

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/storage/application"
	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// LinkPaths are the endpoints serving each link under the public base URL.
type LinkPaths struct {
	Upload, Content, SharePrefix string
}

// Links signs upload links and Access Links with Storage's own secret, so a
// link carries exactly the write or read it was issued for.
type Links struct {
	baseURL string
	paths   LinkPaths
	secret  []byte
}

var _ application.LinkSigningPort = (*Links)(nil)

func NewLinks(baseURL, secret string, paths LinkPaths) *Links {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return &Links{baseURL: baseURL, paths: paths, secret: []byte(secret)}
}

type linkClaims struct {
	Purpose string `json:"p"`
	// Owner and Directory name the Directory: its owner element and name.
	Owner       string `json:"o"`
	Directory   string `json:"dn"`
	Path        string `json:"n"`
	Workspace   bool   `json:"w,omitempty"`
	Replace     bool   `json:"r,omitempty"`
	Uploader    string `json:"u,omitempty"`
	ContentType string `json:"c,omitempty"`
	Download    bool   `json:"d,omitempty"`
	Digest      string `json:"h,omitempty"`
	PreviewCopy bool   `json:"v,omitempty"`
	FileSize    int64  `json:"ws,omitempty"`
	FileTime    int64  `json:"wt,omitempty"`
	ExpiresAt   int64  `json:"e"`
}

const (
	uploadPurpose = "upload"
	accessPurpose = "access"
)

func (l *Links) UploadURL(grant application.UploadGrant, expiresAt time.Time) string {
	claims := linkClaims{
		Purpose: uploadPurpose, Owner: ownerKey(grant.Position.Directory.Owner), Directory: grant.Position.Directory.Name, Path: grant.Position.Name,
		Replace: grant.Replace, Uploader: grant.UploaderUserID, ContentType: grant.ContentType, ExpiresAt: expiresAt.Unix(),
	}
	return l.baseURL + l.signedPath(l.paths.Upload, claims)
}

func (l *Links) VerifyUpload(token string, now time.Time) (application.UploadGrant, error) {
	claims, err := l.verify(token, uploadPurpose, now)
	if err != nil {
		return application.UploadGrant{}, err
	}
	id, err := directoryIDFromDO(claims.Owner, claims.Directory)
	if err != nil {
		return application.UploadGrant{}, application.ErrInvalidLink
	}
	position, err := domain.NewFilePosition(id, claims.Path)
	if err != nil {
		return application.UploadGrant{}, application.ErrInvalidLink
	}
	return application.UploadGrant{Position: position, Replace: claims.Replace, UploaderUserID: claims.Uploader, ContentType: claims.ContentType}, nil
}

func (l *Links) AccessLink(grant application.AccessGrant, expiresAt time.Time) application.AccessLink {
	claims := linkClaims{
		Purpose: accessPurpose, Owner: ownerKey(grant.Directory.Owner), Directory: grant.Directory.Name, Path: grant.Path, Workspace: grant.Workspace,
		Download: grant.Download, Digest: grant.ContentDigest, PreviewCopy: grant.PreviewCopy, ExpiresAt: expiresAt.Unix(),
	}
	if version := grant.WorkspaceVersion; version != nil {
		// A size of zero is a version too, so the time always marks one.
		claims.FileSize, claims.FileTime = version.SizeBytes, version.ModifiedAt.UnixNano()
	}
	path := l.signedPath(l.paths.Content, claims)
	return application.AccessLink{URL: l.baseURL + path, Path: path}
}

func (l *Links) VerifyAccess(token string, now time.Time) (application.AccessGrant, error) {
	claims, err := l.verify(token, accessPurpose, now)
	if err != nil {
		return application.AccessGrant{}, err
	}
	id, err := directoryIDFromDO(claims.Owner, claims.Directory)
	if err != nil {
		return application.AccessGrant{}, application.ErrInvalidLink
	}
	grant := application.AccessGrant{
		Directory: id, Path: claims.Path, Workspace: claims.Workspace, Download: claims.Download, ContentDigest: claims.Digest, PreviewCopy: claims.PreviewCopy,
	}
	if claims.Workspace {
		if _, err := domain.ParseWorkspaceFile(claims.Path); err != nil {
			return application.AccessGrant{}, application.ErrInvalidLink
		}
	} else if domain.ValidateFileName(claims.Path) != nil {
		return application.AccessGrant{}, application.ErrInvalidLink
	}
	if claims.FileTime != 0 {
		grant.WorkspaceVersion = &application.WorkspaceFileVersion{SizeBytes: claims.FileSize, ModifiedAt: time.Unix(0, claims.FileTime).UTC()}
	}
	return grant, nil
}

func (l *Links) ShareURL(token string) string {
	return l.baseURL + l.paths.SharePrefix + url.PathEscape(token)
}

// signedPath is the endpoint with its signed grant, without the public base.
func (l *Links) signedPath(endpoint string, claims linkClaims) string {
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	values := url.Values{}
	values.Set("grant", encoded+"."+l.signature(encoded))
	return endpoint + "?" + values.Encode()
}

func (l *Links) verify(token, purpose string, now time.Time) (linkClaims, error) {
	encoded, signature, ok := strings.Cut(strings.TrimSpace(token), ".")
	if len(l.secret) == 0 || !ok || encoded == "" || !hmac.Equal([]byte(signature), []byte(l.signature(encoded))) {
		return linkClaims{}, application.ErrInvalidLink
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return linkClaims{}, application.ErrInvalidLink
	}
	var claims linkClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Purpose != purpose || claims.ExpiresAt <= now.Unix() || claims.Owner == "" {
		return linkClaims{}, application.ErrInvalidLink
	}
	return claims, nil
}

func (l *Links) signature(encoded string) string {
	mac := hmac.New(sha256.New, l.secret)
	_, _ = mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
