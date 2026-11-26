package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"slices"
	"time"
)

// DirectoryState is a Directory's lifecycle.
type DirectoryState string

const (
	// DirectoryAllocating: its identity, backend, lifetime and Space Token
	// are fixed, but its space is not yet known to exist; nothing is mounted
	// or written.
	DirectoryAllocating DirectoryState = "allocating"
	// DirectoryAllocated: its space exists on its backend; it is mounted and
	// written.
	DirectoryAllocated DirectoryState = "allocated"
	// DirectoryReclaimed: it is gone, never mounted or recreated, and a read
	// answers that it was reclaimed. Terminal.
	DirectoryReclaimed DirectoryState = "reclaimed"
)

// Directory is one owner's space under a name, kept on one backend for one
// lifetime, with the Share Links into it and the mapped paths it is mapped
// at. Where it is mounted and what it is for belong to its caller. A Share
// Link names its file's File ID, so renames and deletions leave the
// Directory unchanged; a read-write grant and a mapping exclude each other
// here.
type Directory struct {
	ID         DirectoryID
	Backend    Backend
	Lifetime   Lifetime
	SpaceToken string
	State      DirectoryState
	ShareLinks []*ShareLink
	// Mappings are the mapped paths its files are shown at.
	Mappings []MappedPath
	// GrantedReadWrite records that a read-write Mount Grant was ever
	// issued; once set, it is never cleared.
	GrantedReadWrite bool
	Version          int
}

// NewDirectory establishes the Directory a located Directory names, with the
// located backend and lifetime and a new Space Token, in Allocating.
func NewDirectory(located LocatedDirectory) (*Directory, error) {
	if _, err := NewLocatedDirectory(located.ID, located.Backend, located.Lifetime); err != nil {
		return nil, err
	}
	return &Directory{ID: located.ID, Backend: located.Backend, Lifetime: located.Lifetime, SpaceToken: newSecret(), State: DirectoryAllocating}, nil
}

// Open is a repeated opening of the existing Directory: it is returned
// unchanged, whatever backend and lifetime the opening located, since the
// first allocation wins. A reclaimed Directory is never recreated.
func (d *Directory) Open() error { return d.admitExisting() }

// AwaitsAllocation reports whether its space must still be allocated, as
// every opening asks while it is Allocating.
func (d *Directory) AwaitsAllocation() bool { return d.State == DirectoryAllocating }

// RecordAllocation records the supplied fact that its space was created on
// its backend. Recording it again changes nothing.
func (d *Directory) RecordAllocation() error {
	switch d.State {
	case DirectoryReclaimed:
		return ErrDirectoryReclaimed
	case DirectoryAllocating:
		d.State = DirectoryAllocated
	}
	return nil
}

// admitUse admits a mount or a write only while its space exists and until
// it is reclaimed.
func (d *Directory) admitUse() error {
	switch d.State {
	case DirectoryAllocated:
		return nil
	case DirectoryReclaimed:
		return ErrDirectoryReclaimed
	default:
		return ErrDirectoryNotAllocated
	}
}

// Reclaimed reports whether it was reclaimed.
func (d *Directory) Reclaimed() bool { return d.State == DirectoryReclaimed }

// AdmitRead answers a read of a reclaimed Directory that it was reclaimed.
func (d *Directory) AdmitRead() error { return d.admitExisting() }

// admitExisting refuses a reclaimed Directory, which never returns.
func (d *Directory) admitExisting() error {
	if d.Reclaimed() {
		return ErrDirectoryReclaimed
	}
	return nil
}

// MountAdmission is the Directory's admission of one mount request: the
// access it grants, read-only or read-write as requested, and the mount
// points its caller names inside it. The Mount Grant handed out carries this
// access at the Directory's backend location once the points are prepared.
type MountAdmission struct {
	Directory   DirectoryID
	ReadOnly    bool
	MountPoints []MountPoint
}

// GrantMount issues a Mount Grant while Allocated: a read-only one, or a
// read-write one while it has no mappings, recording that it was granted
// read-write. A grant is issued, never held: nothing records which mounts
// exist or revokes one.
func (d *Directory) GrantMount(readOnly bool, mountPoints []string) (MountAdmission, error) {
	if err := d.admitUse(); err != nil {
		return MountAdmission{}, err
	}
	if !readOnly && len(d.Mappings) > 0 {
		return MountAdmission{}, ErrDirectoryMapped
	}
	admission := MountAdmission{Directory: d.ID, ReadOnly: readOnly}
	for _, raw := range mountPoints {
		point, err := ParseMountPoint(raw)
		if err != nil {
			return MountAdmission{}, err
		}
		if !slices.Contains(admission.MountPoints, point) {
			admission.MountPoints = append(admission.MountPoints, point)
		}
	}
	if !readOnly {
		d.GrantedReadWrite = true
	}
	return admission, nil
}

// RecordsReadWrite reports whether issuing a grant of this access changes
// the Directory: the first read-write grant records that it was granted.
func (d *Directory) RecordsReadWrite(readOnly bool) bool { return !readOnly && !d.GrantedReadWrite }

// Map adds a mapped path beneath the caller's prefix while Allocated on
// JuiceFS and never granted read-write, on the supplied fact that the path
// overlaps no other mapping. Mapping it again at the same path changes
// nothing.
func (d *Directory) Map(path MappedPath, prefix MappingPrefix, disjoint bool) error {
	if err := prefix.Admit(path); err != nil {
		return err
	}
	if err := d.admitUse(); err != nil {
		return err
	}
	if d.Backend != BackendJuiceFS {
		return ErrBackendNotMappable
	}
	if d.GrantedReadWrite {
		return ErrGrantedReadWrite
	}
	if slices.Contains(d.Mappings, path) {
		return nil
	}
	if !disjoint {
		return ErrMappingOverlaps
	}
	d.Mappings = append(d.Mappings, path)
	return nil
}

// Unmap removes a mapped path beneath the caller's prefix; removing one it
// does not have changes nothing.
func (d *Directory) Unmap(path MappedPath, prefix MappingPrefix) error {
	if err := prefix.Admit(path); err != nil {
		return err
	}
	d.Mappings = slices.DeleteFunc(d.Mappings, func(mapped MappedPath) bool { return mapped == path })
	return nil
}

// Mapped reports whether it is mapped at any path.
func (d *Directory) Mapped() bool { return len(d.Mappings) > 0 }

// MappedAt reports whether it is mapped at the path.
func (d *Directory) MappedAt(path MappedPath) bool { return slices.Contains(d.Mappings, path) }

// AdmitCommit admits again, under the Directory's lock, a file change
// admitted before it: only while Allocated, so none lands in a Directory
// reclaimed meanwhile.
func (d *Directory) AdmitCommit() error { return d.admitUse() }

// AdmitFileWrite admits a Create, Replace, Rename or Delete only while
// Allocated, only on its own Space Token and only when the command names its
// writer. Whether the target position is free is answered by its backend
// when the write is carried out.
func (d *Directory) AdmitFileWrite(token string, writer FileWriter) error {
	if err := d.admitToken(token); err != nil {
		return err
	}
	if !writer.named() {
		return ErrFileWriterRequired
	}
	return nil
}

// AdmitContentReplacement admits switching prepared content in as its whole
// current content, only while Allocated, on its own Space Token, and while
// the content the replacement expects is the current one (Conditional
// replacement).
func (d *Directory) AdmitContentReplacement(token string, expected ContentExpectation, current ContentRevision) error {
	if err := d.admitToken(token); err != nil {
		return err
	}
	return expected.Admit(current)
}

func (d *Directory) admitToken(token string) error {
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(d.SpaceToken)) != 1 {
		return ErrSpaceTokenRejected
	}
	return d.admitUse()
}

// Reclaim accepts reclamation only for its Work's lifetime, ending its
// mappings. A repeated request changes nothing; removing its content is
// carried out outside the Directory once publications copying from it
// finish.
func (d *Directory) Reclaim() error {
	if d.Lifetime != LifetimeWork {
		return ErrPermanentDirectory
	}
	d.State = DirectoryReclaimed
	d.Mappings = nil
	return nil
}

// CreateShareLink adds an active Share Link to the file with the supplied
// File ID, which the caller found to be an Attachment or Artifact of this
// Directory, also after the work the Directory serves has ended.
func (d *Directory) CreateShareLink(file FileID, createdBy Actor, now time.Time) (*ShareLink, error) {
	if file == "" {
		return nil, ErrShareLinkNotFound
	}
	if err := createdBy.Validate(); err != nil {
		return nil, err
	}
	link := &ShareLink{Token: newSecret(), File: file, CreatedBy: createdBy.trimmed(), CreatedAt: now.UTC(), State: ShareLinkActive}
	d.ShareLinks = append(d.ShareLinks, link)
	return link, nil
}

// RevokeShareLinks revokes the active Share Links of the file with the
// supplied File ID, every one when createdBy names no one, or only those
// createdBy made, and reports how many it revoked and how many stay active.
func (d *Directory) RevokeShareLinks(file FileID, createdBy Actor) (revoked, remaining int) {
	createdBy = createdBy.trimmed()
	for _, link := range d.ActiveShareLinks(file) {
		if createdBy != (Actor{}) && link.CreatedBy != createdBy {
			remaining++
			continue
		}
		link.Revoke()
		revoked++
	}
	return revoked, remaining
}

// ShareLink returns the Directory's link with the token, active or revoked.
func (d *Directory) ShareLink(token string) *ShareLink {
	for _, link := range d.ShareLinks {
		if link.Token == token {
			return link
		}
	}
	return nil
}

// ActiveShareLinks returns the active links of the file with the File ID; a
// file without one has none.
func (d *Directory) ActiveShareLinks(file FileID) []*ShareLink {
	if file == "" {
		return nil
	}
	var links []*ShareLink
	for _, link := range d.ShareLinks {
		if link.Active() && link.File == file {
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

// ShareLink is an unguessable link to one file of its Directory, readable by
// anyone holding it. It names the file's File ID, so it reaches the file
// wherever a rename moves it and never a later file at a freed name.
type ShareLink struct {
	Token string
	File  FileID
	// CreatedBy is the person or Application that created it.
	CreatedBy Actor
	CreatedAt time.Time
	State     ShareLinkState
}

// Active reports whether reading through the link returns the current
// content of its file, or answers that the file is gone.
func (l *ShareLink) Active() bool { return l.State == ShareLinkActive }

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
