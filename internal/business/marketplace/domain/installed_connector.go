package domain

import (
	"errors"

	"github.com/google/uuid"
)

var ErrConnectorInstallationNotFound = errors.New("personal Connector installation not found")

// InstalledConnector is one user's installation of one catalog Connector. It
// owns only enablement; Provider authorization is neither a condition nor state.
type InstalledConnector struct {
	// ID identifies this persisted incarnation. Lookup and uniqueness remain
	// OwnerID x ConnectorID, including across uninstall and reinstall.
	ID          string `validate:"required,uuid"`
	OwnerID     string `validate:"required,max=255"`
	ConnectorID string `validate:"required,uuid"`
	State       InstallationState
	Version     int
}

// InstallConnector establishes an Enabled installation for an application-
// authorized user after the catalog entry admits personal installation.
func InstallConnector(ownerID string, connector *Connector) (*InstalledConnector, error) {
	if err := connector.Validate(); err != nil {
		return nil, err
	}
	if err := connector.AdmitInstallation(); err != nil {
		return nil, err
	}
	installation := &InstalledConnector{ID: uuid.Must(uuid.NewV7()).String(), OwnerID: ownerID, ConnectorID: connector.ID, State: Enabled}
	return installation, installation.Validate()
}

func (c *InstalledConnector) Validate() error {
	if c == nil || domainValidator.Struct(c) != nil || (c.State != Enabled && c.State != Disabled) || c.Version < 0 {
		return ErrInvalidInstallation
	}
	return nil
}

func (c *InstalledConnector) Enable()  { c.State = Enabled }
func (c *InstalledConnector) Disable() { c.State = Disabled }

// Uninstall ends the installation; the application deletes the root. It
// cancels no ProviderAuthorization and uninstalls no Plugin.
func (c *InstalledConnector) Uninstall() {}
