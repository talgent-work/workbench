package domain

import (
	"errors"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"regexp"

	"github.com/google/uuid"
)

var (
	ErrInvalidInstallation    = errors.New("invalid personal plugin installation")
	ErrInstallationNotFound   = errors.New("personal plugin installation not found")
	ErrConcurrentModification = errors.New("personal plugin installation changed concurrently")
	pluginSlug                = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

type InstallationState uint8

const (
	Enabled InstallationState = iota + 1
	Disabled
)

type InstalledPlugin struct {
	// ID identifies this persisted installation incarnation. Personal lookup and
	// uniqueness remain OwnerID x Slug, including across uninstall/reinstall.
	ID                  string `validate:"required,uuid"`
	OwnerID             string `validate:"required,max=255"`
	Slug                string `validate:"required,max=128"`
	State               InstallationState
	ManualConfiguration Configuration
	Version             int
}

// Install is called with an application-authorized user, never an owner chosen
// independently by a public request. It creates no runtime installation.
func Install(ownerID, slug string, manual Configuration, declarations ParameterDeclarations) (*InstalledPlugin, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	plugin := &InstalledPlugin{ID: id.String(), OwnerID: ownerID, Slug: slug, State: Enabled, ManualConfiguration: manual.Clone()}
	if err := plugin.Validate(); err != nil {
		return nil, err
	}
	if err := plugin.validateChanges(manual, declarations); err != nil {
		return nil, err
	}
	return plugin, nil
}

func (p *InstalledPlugin) Validate() error {
	if p == nil {
		return ErrInvalidInstallation
	}
	if err := domainValidator.Struct(p); err != nil || !pluginconfig.ValidPluginName(p.Slug) || (p.State != Enabled && p.State != Disabled) || p.Version < 0 {
		return ErrInvalidInstallation
	}
	return nil
}

func (p *InstalledPlugin) Enable()  { p.State = Enabled }
func (p *InstalledPlugin) Disable() { p.State = Disabled }

func (p *InstalledPlugin) SetConfiguration(values Configuration, declarations ParameterDeclarations) error {
	if err := p.validateChanges(values, declarations); err != nil {
		return err
	}
	configuration := p.ManualConfiguration.Clone()
	for name, value := range values.Clone() {
		configuration[name] = value
	}
	if _, err := configuration.Effective(declarations); err != nil {
		return err
	}
	p.ManualConfiguration = configuration
	return nil
}

func (p *InstalledPlugin) DeleteConfiguration(names []string, declarations ParameterDeclarations) error {
	configuration := p.ManualConfiguration.Clone()
	for _, name := range names {
		delete(configuration, name)
	}
	if _, err := configuration.Effective(declarations); err != nil {
		return err
	}
	p.ManualConfiguration = configuration
	return nil
}

// Uninstall ends the personal installation. The application deletes this root
// through its repository; there is no retained Uninstalled lifecycle state.
func (p *InstalledPlugin) Uninstall() { p.ManualConfiguration = nil }

func (p *InstalledPlugin) validateChanges(values Configuration, declarations ParameterDeclarations) error {
	for name := range values {
		if _, exists := declarations[name]; !exists {
			return invalidParameter(name, "parameter is not declared")
		}
	}
	configuration := p.ManualConfiguration.Clone()
	for name, value := range values {
		configuration[name] = value
	}
	_, err := configuration.Effective(declarations)
	return err
}
