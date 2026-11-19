package domain

import "strings"

// OwnerKind says who owns a Connector: the platform, a person or an
// Application, which stands where a person stands (Application Owner manages).
type OwnerKind uint8

const (
	OwnerPlatform OwnerKind = iota + 1
	OwnerPerson
	OwnerApplication
)

// ConnectorOwner names the platform or the owning person or Application; it
// never changes.
type ConnectorOwner struct {
	Kind OwnerKind
	// ID is the person's or Application's identity; empty for the platform.
	ID string
}

// PlatformOwner owns the Platform Connectors the deployment provides.
var PlatformOwner = ConnectorOwner{Kind: OwnerPlatform}

// Platform reports whether the deployment provides the Connector.
func (o ConnectorOwner) Platform() bool { return o.Kind == OwnerPlatform }

// Is reports whether owner, a person or Application identity, is this owner.
func (o ConnectorOwner) Is(owner string) bool {
	return !o.Platform() && owner != "" && o.ID == owner
}

func (o ConnectorOwner) validate() error {
	switch o.Kind {
	case OwnerPlatform:
		if o.ID != "" {
			return ErrInvalidConnector
		}
	case OwnerPerson, OwnerApplication:
		if strings.TrimSpace(o.ID) == "" || len(o.ID) > 255 {
			return ErrInvalidConnector
		}
	default:
		return ErrInvalidConnector
	}
	return nil
}
