package query

import (
	"fmt"
	"strings"
	"unicode"
)

// The Work Caller kinds the core defines: a person and an Application, each
// starting Standalone Works for itself. Every other kind is defined by the
// extension that starts Works under it, and that extension decides who reads
// them. Observability reads these facts as Agent writes them into telemetry
// and keeps its own copy of the kinds rather than depend on Agent's model.
const (
	workCallerKindPerson      = "person"
	workCallerKindApplication = "application"
	maxFactKindLength         = 32
	maxFactIdentityLength     = 255
)

// WorkCaller is the party in whose name a Work was started: a person, an
// Application, or an object of an extension-defined kind. The zero value
// names none.
type WorkCaller struct{ Kind, ID string }

// PersonCaller is the Work Caller of a person's Standalone Works.
func PersonCaller(id string) WorkCaller { return WorkCaller{Kind: workCallerKindPerson, ID: id} }

// ApplicationCaller is the Work Caller of an Application's Standalone Works.
func ApplicationCaller(id string) WorkCaller {
	return WorkCaller{Kind: workCallerKindApplication, ID: id}
}

// Present tells whether a Work Caller is named.
func (c WorkCaller) Present() bool { return c != WorkCaller{} }

// Standalone tells whether a Work under this caller is a Standalone Work,
// which Agent's Work access admits readers to.
func (c WorkCaller) Standalone() bool {
	return c.Kind == workCallerKindPerson || c.Kind == workCallerKindApplication
}

// String is the caller as telemetry carries it, "<kind>:<id>"; empty for
// none.
func (c WorkCaller) String() string { return joinFact(c.Kind, c.ID) }

// ParseWorkCaller reads "<kind>:<id>" as telemetry carries it, split at the
// first colon; a value without one names no caller.
func ParseWorkCaller(value string) WorkCaller {
	kind, id := splitFact(value)
	return WorkCaller{Kind: kind, ID: id}
}

// WorkKey is the name an extension gave a Work to find it again. The zero
// value names none.
type WorkKey struct{ Kind, ID string }

// Present tells whether a Work Key is named.
func (k WorkKey) Present() bool { return k != WorkKey{} }

// String is the key as telemetry carries it, "<kind>:<id>"; empty for none.
func (k WorkKey) String() string { return joinFact(k.Kind, k.ID) }

// ParseWorkKey reads "<kind>:<id>" as telemetry carries it; a value without
// a colon names no key.
func ParseWorkKey(value string) WorkKey {
	kind, id := splitFact(value)
	return WorkKey{Kind: kind, ID: id}
}

// ValidExtensionKind tells whether kind is a Work Caller kind an extension
// may define: one Agent admits, and neither of the core's kinds.
func ValidExtensionKind(kind string) bool {
	return validKind(kind) && kind != workCallerKindPerson && kind != workCallerKindApplication
}

// extensionCaller admits a requested Work Caller of an extension-defined
// kind, the only kind a read names a Work Caller by: a Standalone Work is
// named by itself, a person's and an Application's usage by their own reads.
func extensionCaller(caller WorkCaller) (WorkCaller, error) {
	if err := validateFact(caller.Kind, caller.ID, "work_caller"); err != nil {
		return WorkCaller{}, err
	}
	if caller.Standalone() {
		return WorkCaller{}, fmt.Errorf("%w: work_caller names an extension-defined kind; a Standalone Work is named without one", ErrInvalidQuery)
	}
	return caller, nil
}

// requestedWorkKey admits no Work Key, or a complete one.
func requestedWorkKey(key WorkKey) (WorkKey, error) {
	if !key.Present() {
		return key, nil
	}
	return key, validateFact(key.Kind, key.ID, "work_key")
}

// validateFact admits a requested fact exactly as supplied, never trimmed,
// under the rules Agent and Account apply: a kind of at most 32 lowercase
// letters, digits, "_" or "-", which holds no colon, and an identity of 1 to
// 255 bytes with no surrounding whitespace or control character.
func validateFact(kind, id, name string) error {
	if !validKind(kind) {
		return fmt.Errorf("%w: %s kind %q", ErrInvalidQuery, name, kind)
	}
	switch {
	case id == "" || len(id) > maxFactIdentityLength:
		return fmt.Errorf("%w: %s id needs 1 to %d bytes", ErrInvalidQuery, name, maxFactIdentityLength)
	case strings.TrimSpace(id) != id:
		return fmt.Errorf("%w: %s id has surrounding whitespace", ErrInvalidQuery, name)
	case strings.IndexFunc(id, unicode.IsControl) >= 0:
		return fmt.Errorf("%w: %s id holds a control character", ErrInvalidQuery, name)
	}
	return nil
}

func validKind(kind string) bool {
	return kind != "" && len(kind) <= maxFactKindLength && strings.TrimFunc(kind, kindCharacter) == ""
}

func kindCharacter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func joinFact(kind, id string) string {
	if kind == "" && id == "" {
		return ""
	}
	return kind + ":" + id
}

func splitFact(value string) (string, string) {
	kind, id, found := strings.Cut(value, ":")
	if !found {
		return "", ""
	}
	return kind, id
}
