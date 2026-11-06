package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"slices"
	"strings"
	"time"
)

// FileSpace is one owner's persistent space for its Attachments and
// Artifacts and the Share Links into it. Renames, deletions and Share Link
// changes of one space are serialized here, so every active Share Link
// points at an existing File position. Its owner is the identity its caller
// named; Storage never interprets it.
type FileSpace struct {
	Owner      string
	SpaceToken string
	ShareLinks []*ShareLink
	Version    int
}

// OpenFileSpace establishes the space of an owner, issuing its Space Token.
// Reopening an existing space is the caller's lookup: the existing space is
// returned with its token unchanged.
func OpenFileSpace(owner string) (*FileSpace, error) {
	owner, err := ParseOwner(owner)
	if err != nil {
		return nil, err
	}
	return &FileSpace{Owner: owner, SpaceToken: newSecret()}, nil
}

// inputSourcePrefix marks the opaque input source naming a File Space.
const inputSourcePrefix = "file-space/"

// InputSource is the opaque reference a Work's input source holds for the space.
func (s *FileSpace) InputSource() string { return inputSourcePrefix + s.Owner }

// OwnerOfInputSource reads the owner of an input source a File Space issued.
func OwnerOfInputSource(source string) (string, error) {
	owner, ok := strings.CutPrefix(source, inputSourcePrefix)
	if !ok {
		return "", ErrSpaceNotOpen
	}
	if _, err := ParseOwner(owner); err != nil || owner != strings.TrimSpace(owner) {
		return "", ErrSpaceNotOpen
	}
	return owner, nil
}

// AdmitFileWrite admits a Create, Replace, Rename or Delete only on the
// space's own Space Token and only when it names its writer. Whether the
// target position is free is answered by persistent storage when the write is
// carried out.
func (s *FileSpace) AdmitFileWrite(token string, writer FileWriter) error {
	if err := s.AdmitSpaceToken(token); err != nil {
		return err
	}
	if !writer.named() {
		return ErrFileWriterRequired
	}
	return nil
}

// AdmitSpaceToken admits only the space's own Space Token, the credential a
// write or the space's input source is presented with.
func (s *FileSpace) AdmitSpaceToken(token string) error {
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.SpaceToken)) != 1 {
		return ErrSpaceTokenRejected
	}
	return nil
}

// RecordRename moves the active Share Links at a renamed file's old position
// to the new one, on the fact that the rename was carried out.
func (s *FileSpace) RecordRename(from, to FilePosition) {
	for _, link := range s.ShareLinks {
		if link.Active() && link.Target == from {
			link.Retarget(to)
		}
	}
}

// RevokeStaleShareLinks revokes the supplied links still active at a
// position the caller found free before a file took it. Such a link was left
// by a deletion that was never recorded; it must not reach the new file.
// Links not supplied, created once the new file existed, stay active.
func (s *FileSpace) RevokeStaleShareLinks(position FilePosition, stale []string) {
	for _, link := range s.ActiveShareLinks(position) {
		if slices.Contains(stale, link.Token) {
			link.Revoke()
		}
	}
}

// ShareLinkTokens are the tokens of the active links at a position.
func (s *FileSpace) ShareLinkTokens(position FilePosition) []string {
	var tokens []string
	for _, link := range s.ActiveShareLinks(position) {
		tokens = append(tokens, link.Token)
	}
	return tokens
}

// RecordDeletion revokes the active Share Links at a deleted position, on the
// fact that the deletion was carried out.
func (s *FileSpace) RecordDeletion(position FilePosition) {
	for _, link := range s.ShareLinks {
		if link.Active() && link.Target == position {
			link.Revoke()
		}
	}
}

// CreateShareLink adds an active Share Link to a position the caller found
// existing, also after the work the space holds has ended.
func (s *FileSpace) CreateShareLink(target FilePosition, createdBy string, now time.Time) (*ShareLink, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if target.Owner != s.Owner {
		return nil, ErrShareLinkNotFound
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		return nil, ErrSharerRequired
	}
	link := &ShareLink{Token: newSecret(), Target: target, CreatedBy: createdBy, CreatedAt: now.UTC(), State: ShareLinkActive}
	s.ShareLinks = append(s.ShareLinks, link)
	return link, nil
}

// RevokeShareLinks revokes the active Share Links at a position, every one
// or only those createdBy made when it is set, and reports how many it
// revoked and how many stay active.
func (s *FileSpace) RevokeShareLinks(target FilePosition, createdBy string) (revoked, remaining int) {
	createdBy = strings.TrimSpace(createdBy)
	for _, link := range s.ActiveShareLinks(target) {
		if createdBy != "" && link.CreatedBy != createdBy {
			remaining++
			continue
		}
		link.Revoke()
		revoked++
	}
	return revoked, remaining
}

// ShareLink returns the space's link with the token, active or revoked.
func (s *FileSpace) ShareLink(token string) *ShareLink {
	for _, link := range s.ShareLinks {
		if link.Token == token {
			return link
		}
	}
	return nil
}

// ActiveShareLinks returns the active links at a position.
func (s *FileSpace) ActiveShareLinks(position FilePosition) []*ShareLink {
	var links []*ShareLink
	for _, link := range s.ShareLinks {
		if link.Active() && link.Target == position {
			links = append(links, link)
		}
	}
	return links
}

// ShareLinkState is a Share Link's lifecycle.
type ShareLinkState string

const (
	ShareLinkActive  ShareLinkState = "active"
	ShareLinkRevoked ShareLinkState = "revoked"
)

// ShareLink is an unguessable link to the file at one position of its space,
// readable by anyone holding it.
type ShareLink struct {
	Token     string
	Target    FilePosition
	CreatedBy string
	CreatedAt time.Time
	State     ShareLinkState
}

// Active reports whether reading through the link returns the content at Target.
func (l *ShareLink) Active() bool { return l.State == ShareLinkActive }

// Retarget follows a renamed file while the link is active.
func (l *ShareLink) Retarget(target FilePosition) {
	if l.Active() {
		l.Target = target
	}
}

// Revoke ends the link; revocation is terminal.
func (l *ShareLink) Revoke() { l.State = ShareLinkRevoked }

// newSecret is an unguessable URL-safe token of 256 bits.
func newSecret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
