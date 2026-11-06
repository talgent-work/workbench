package domain

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ManagedDesks is the room a newly registered Managed Computer gives: one
// Desk, rejecting a run while it is taken, until its owner sets other rules.
const ManagedDesks = 1

// MaxManagedDesks bounds how many Works its owner lets a Managed Computer run
// at once.
const MaxManagedDesks = 32

// MaxNameLength bounds a Computer's display name, in characters.
const MaxNameLength = 64

// Registration is one approved registration of a machine.
type Registration struct {
	ComputerID string
	OwnerID    string
	Name       string
	Credential Credential
	Now        time.Time
}

// Register registers a machine for the person who approved it, as a new
// Managed Computer with the Computer Credential issued once. It is not
// reachable until its machine connects.
func Register(registration Registration) (*Computer, error) {
	name, err := computerName(registration.Name)
	if err != nil {
		return nil, err
	}
	if registration.Now.IsZero() || len(registration.Credential.Digest) != sha256.Size*2 || !registration.Credential.ExpiresAt.After(registration.Now) {
		return nil, fmt.Errorf("%w: a registration needs a time and an unexpired credential", ErrInvalidComputer)
	}
	computer := &Computer{
		ID: strings.TrimSpace(registration.ComputerID), Kind: KindManaged, OwnerID: strings.TrimSpace(registration.OwnerID), Name: name,
		Capacity: Room{Desks: ManagedDesks}, Policy: AdmissionReject,
		Presence:   Presence{UnreachableSince: registration.Now},
		Credential: registration.Credential, State: StateActive, Events: NewEventCollection(),
	}
	if err := computer.Validate(); err != nil {
		return nil, err
	}
	return computer, nil
}

func computerName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", fmt.Errorf("%w: a name has 1 to %d characters", ErrInvalidComputer, MaxNameLength)
	}
	return name, nil
}

// Rename gives a Managed Computer the name its owner chose.
func (c *Computer) Rename(name string) error {
	if c.Kind != KindManaged {
		return fmt.Errorf("%w: the platform names a Cloud", ErrInvalidComputer)
	}
	if c.State == StateRemoved {
		return ErrComputerRemoved
	}
	renamed, err := computerName(name)
	if err != nil {
		return err
	}
	c.Name = renamed
	return nil
}

// LoseReach records that the platform lost reach of a Managed Computer at a
// time; reach already lost keeps its first time.
func (c *Computer) LoseReach(at time.Time) error {
	if c.Kind != KindManaged {
		return fmt.Errorf("%w: a Cloud is reachable while the platform runs", ErrInvalidComputer)
	}
	if at.IsZero() {
		return fmt.Errorf("%w: losing reach needs its time", ErrInvalidComputer)
	}
	if !c.Presence.Reachable {
		return nil
	}
	c.Presence = Presence{UnreachableSince: at}
	c.record(ComputerBecameUnreachable{ComputerID: c.ID, Since: at})
	return nil
}

// RegainReach records that the platform reaches a Managed Computer again.
func (c *Computer) RegainReach() error {
	if c.Kind != KindManaged {
		return fmt.Errorf("%w: a Cloud is reachable while the platform runs", ErrInvalidComputer)
	}
	if c.State == StateRemoved {
		return ErrComputerRemoved
	}
	if c.Presence.Reachable {
		return nil
	}
	c.Presence = Presence{Reachable: true}
	c.record(ComputerBecameReachable{ComputerID: c.ID})
	return nil
}

// ReportVersions records the versions a Managed Computer's machine reports.
func (c *Computer) ReportVersions(versions ReportedVersions) error {
	if c.Kind != KindManaged {
		return fmt.Errorf("%w: a Cloud reports no versions", ErrInvalidComputer)
	}
	versions.TalgentComputer, versions.ClaudeCode = strings.TrimSpace(versions.TalgentComputer), strings.TrimSpace(versions.ClaudeCode)
	if versions.TalgentComputer == "" {
		return fmt.Errorf("%w: a machine reports its Talgent Computer version", ErrInvalidComputer)
	}
	if err := domainValidator.Struct(versions); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidComputer, err)
	}
	c.Versions = versions
	return nil
}

// ReportSandbox records whether a Managed Computer's machine reports that it
// can provide Claude Code's sandbox, as each of its connections does: native
// Windows cannot, and a machine missing what the sandbox needs cannot until
// it is installed.
func (c *Computer) ReportSandbox(available bool) error {
	if c.Kind != KindManaged {
		return fmt.Errorf("%w: a Cloud reports no sandbox", ErrInvalidComputer)
	}
	c.Sandbox = available
	return nil
}
