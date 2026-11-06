// Package storagepath is Storage's Originals Mount Contract: the
// layout of the standing read-only mount of every File Space, by owner
// identity, file kind and name, and the extended attributes kept with each
// file. The reader interprets owner identities itself and reads originals in
// place by this layout alone. Beside it only the Access Link endpoint is
// published, which the native API serves to Works.
package storagepath

import (
	"path"
	"strings"
)

// Kind directories inside a File Space's directory.
const (
	AttachmentsDirectory = "attachments"
	ArtifactsDirectory   = "artifacts"
)

// Space is a File Space's directory relative to the originals mount root:
// its owner identity, which is always one visible path element.
func Space(owner string) string {
	return component(owner)
}

// Attachments is the directory of a File Space's Attachments.
func Attachments(owner string) string {
	return path.Join(Space(owner), AttachmentsDirectory)
}

// Artifacts is the directory of a File Space's Artifacts.
func Artifacts(owner string) string {
	return path.Join(Space(owner), ArtifactsDirectory)
}

// Extended attributes kept with each file. Values are UTF-8 text; an absent
// attribute means the fact was not recorded.
const (
	// AttributeUploader is the account id of the human who uploaded an Attachment.
	AttributeUploader = "user.talgent.uploader"
	// AttributeWork is the Work that published an Artifact.
	AttributeWork = "user.talgent.work"
	// AttributeContentType is the media type declared at upload.
	AttributeContentType = "user.talgent.content_type"
)

// ContentPath is where Storage serves Access Links, on the platform's
// public address and on the native API alike.
const ContentPath = "/v1/storage/content"

// component keeps an owner a single visible path element; Storage admits no
// other owner, and anything else maps to a name that cannot escape its parent.
func component(identity string) string {
	if identity == "" || strings.HasPrefix(identity, ".") || strings.ContainsAny(identity, "/\\\x00") {
		return "invalid"
	}
	return identity
}
