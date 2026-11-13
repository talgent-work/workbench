package domain

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidWorkCaller = errors.New("agent: invalid Work Caller")
	ErrInvalidWorkKey    = errors.New("agent: invalid Work Key")
	ErrInvalidFileHome   = errors.New("agent: invalid File Home")
)

// The Work Caller kinds Agent itself defines: a person and an Application,
// each starting Standalone Works for itself. Every other kind is defined by
// the extension that starts Works under it, and Agent never interprets it.
const (
	CallerKindPerson      = "person"
	CallerKindApplication = "application"
)

const (
	maxFactKindLength     = 32
	maxFactIdentityLength = 255
)

// WorkCaller is the party in whose name a Work is started, as a kind and an
// identity, fixed at creation: a person, an Application, or an object of an
// extension-defined kind. Every Work has one.
type WorkCaller struct {
	Kind, ID string
}

// NewWorkCaller admits a kind with an identity.
func NewWorkCaller(kind, id string) (WorkCaller, error) {
	caller := WorkCaller{Kind: kind, ID: id}
	return caller, caller.Validate()
}

// Validate requires a kind and an identity.
func (c WorkCaller) Validate() error {
	return validateKindIdentity(c.Kind, c.ID, ErrInvalidWorkCaller)
}

// ExtensionDefined tells whether an extension defines the caller's kind: a
// Work started under it is an Extension Work. A caller without a kind is of
// none.
func (c WorkCaller) ExtensionDefined() bool {
	return c.Kind != "" && c.Kind != CallerKindPerson && c.Kind != CallerKindApplication
}

// StartsStandaloneWork tells whether a Work under this caller is a Standalone
// Work: its caller is a person or an Application, the owner starting it for
// itself.
func (c WorkCaller) StartsStandaloneWork() bool {
	return c.Kind == CallerKindPerson || c.Kind == CallerKindApplication
}

// String is the caller as "<kind>:<id>", the value a Directory Locator and
// telemetry carry.
func (c WorkCaller) String() string { return c.Kind + ":" + c.ID }

// WorkKey is the optional name an extension gives a Work to find it again,
// as a kind it defines and an identity, fixed at creation. Within one
// AgentDefinition a Work Key has at most one Work for good, a closed one
// included; Agent applies that uniqueness without interpreting the key. The
// zero value means none.
type WorkKey struct {
	Kind, ID string
}

// NewWorkKey admits no Work Key, or a kind with an identity.
func NewWorkKey(kind, id string) (WorkKey, error) {
	key := WorkKey{Kind: kind, ID: id}
	return key, key.Validate()
}

// Present tells whether the Work has a Work Key.
func (k WorkKey) Present() bool { return k != WorkKey{} }

// Validate accepts no Work Key, or a kind together with an identity.
func (k WorkKey) Validate() error {
	if !k.Present() {
		return nil
	}
	return validateKindIdentity(k.Kind, k.ID, ErrInvalidWorkKey)
}

// String is the key as "<kind>:<id>"; empty for none.
func (k WorkKey) String() string {
	if !k.Present() {
		return ""
	}
	return k.Kind + ":" + k.ID
}

// FileHome is the optional owner of the shared File Spaces a Work reads its
// inputs from and publishes into, as a kind its extension defines and an
// identity, fixed at creation. A Work without one keeps both in its own File
// Spaces. The zero value means none.
type FileHome struct {
	Kind, ID string
}

// NewFileHome admits no File Home, or a kind with an identity.
func NewFileHome(kind, id string) (FileHome, error) {
	home := FileHome{Kind: kind, ID: id}
	return home, home.Validate()
}

// Present tells whether the Work has a File Home.
func (h FileHome) Present() bool { return h != FileHome{} }

// Validate accepts no File Home, or a kind together with an identity.
func (h FileHome) Validate() error {
	if !h.Present() {
		return nil
	}
	return validateKindIdentity(h.Kind, h.ID, ErrInvalidFileHome)
}

// String is the File Home as "<kind>:<id>"; empty for none.
func (h FileHome) String() string {
	if !h.Present() {
		return ""
	}
	return h.Kind + ":" + h.ID
}

// validateKindIdentity admits a kind of at most 32 lowercase letters, digits,
// "_" or "-", so it holds no ":", which splits "<kind>:<id>" at its first
// colon, and no case variant of a kind Agent defines; and an opaque identity
// of 1 to 255 bytes without NUL. How a fact travels, such as in telemetry, is
// for whatever carries it to encode.
func validateKindIdentity(kind, id string, invalid error) error {
	if kind == "" || len(kind) > maxFactKindLength || strings.TrimFunc(kind, kindCharacter) != "" {
		return fmt.Errorf("%w: kind %q", invalid, kind)
	}
	if id == "" || len(id) > maxFactIdentityLength || strings.ContainsRune(id, 0) {
		return fmt.Errorf("%w: identity", invalid)
	}
	return nil
}

func kindCharacter(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-'
}
