//go:build integration

package infrastructure_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// corePluginRenameMigration is the one-off script that renames the core
// plugin's stored slug; it and this test are removed once every environment
// ran it.
const corePluginRenameMigration = "../../../../deploy/middleware/migrations/20261111-core-plugin-rename.sql"

// releaseCatalog releases one Plugin, declaring one role, at one version.
type releaseCatalog struct{ slug, role, version string }

func (releaseCatalog) ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error) {
	return nil, nil
}

func (c releaseCatalog) ResolvePlugin(_ context.Context, _ string, slug string) (domain.PluginRelease, error) {
	if slug != c.slug {
		return domain.PluginRelease{}, fmt.Errorf("fixture: no release of %s", slug)
	}
	return domain.PluginRelease{CatalogName: "talgent", Slug: slug, Source: "public", Version: c.version, RetrievalRef: "archive:" + slug + "-" + c.version,
		NativeEntry: []byte(fmt.Sprintf(`{"name":%q,"version":%q}`, slug, c.version)),
		Skills:      []domain.SkillComponent{{Name: slug + ":interviewing", Description: "Interview the owner"}},
		Agents:      []domain.AgentComponent{{Name: c.role, Description: "Work executor"}}}, nil
}

func (releaseCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}

// A deployment holding the old core plugin slug, talgent-runtime, in a
// definition, a Work and personal installations resolves talgent after the
// script: the definition requires it and starts with its role, the Work's
// next start takes up its current release from a catalog that lists only
// talgent, and each person's installation keeps its state and values; a
// person who already installed talgent keeps that installation alone. Applying
// the script again changes nothing.
func TestCorePluginRenameMigrationResolvesTalgentEverywhere(t *testing.T) {
	const (
		oldSlug, oldRole = "talgent-runtime", "talgent-runtime:work-executor"
		newSlug, newRole = "talgent", "talgent:work-executor"
	)
	ctx := context.Background()
	before := newBuiltInFixture(t, configuredBuiltIns(t, []string{oldSlug}, nil), releaseCatalog{slug: oldSlug, role: oldRole, version: "1"})
	definitionID := before.projectDefinition(t, oldRole, oldSlug)
	workID, err := before.createProjectWork(definitionID)
	require.NoError(t, err)

	db := before.db.DB().DB
	installations := map[string]struct {
		state         int
		configuration string
	}{"alice": {1, `{"token":"secret"}`}, "bob": {2, `{}`}}
	for user, installation := range installations {
		_, err = db.ExecContext(ctx, `INSERT INTO marketplace_installed_plugin (id, user_id, slug, state, configuration) VALUES (?, ?, ?, ?, ?)`,
			uuid.NewString(), user, oldSlug, installation.state, installation.configuration)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO marketplace_installed_plugin (id, user_id, slug, state, configuration) VALUES (?, 'alice', 'other', 1, '{}')`, uuid.NewString())
	require.NoError(t, err)
	// carol already installed talgent: that installation stays and her
	// talgent-runtime one goes, leaving one installation per slug.
	for _, row := range [][3]any{{"talgent-runtime", 2, `{"mode":"old"}`}, {"talgent", 1, `{"mode":"new"}`}} {
		_, err = db.ExecContext(ctx, `INSERT INTO marketplace_installed_plugin (id, user_id, slug, state, configuration) VALUES (?, 'carol', ?, ?, ?)`, uuid.NewString(), row[0], row[1], row[2])
		require.NoError(t, err)
	}

	script, err := os.ReadFile(corePluginRenameMigration)
	require.NoError(t, err)
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	for range 2 {
		_, err = conn.ExecContext(ctx, string(script))
		require.NoError(t, err, "the script is repeatable")
	}

	definition, err := before.app.Definitions.Get(ctx, definitionID)
	require.NoError(t, err)
	require.Equal(t, newRole, definition.Capabilities.StartupAgent)
	require.Equal(t, []string{newSlug}, definition.Capabilities.RequiredPlugins)

	work, err := before.app.Works.Get(ctx, workID)
	require.NoError(t, err)
	require.Equal(t, newRole, work.CapabilitySnapshot.StartupAgent)
	require.Len(t, work.CapabilitySnapshot.Plugins, 1)
	retained := work.CapabilitySnapshot.Plugins[0].Release
	require.Equal(t, newSlug, retained.Slug)
	require.Equal(t, []domain.AgentComponent{{Name: newRole, Description: "Work executor"}}, retained.Agents)
	require.Equal(t, newSlug+":interviewing", retained.Skills[0].Name)
	require.NoError(t, work.CapabilitySnapshot.Validate())

	current, err := command.NewCatalogPluginUpgrades(currentCatalog(newSlug, newRole), slog.New(slog.NewTextHandler(io.Discard, nil))).
		CurrentReleases(ctx, "owner", work.CapabilitySnapshot)
	require.NoError(t, err)
	require.Len(t, current, 1, "the Work's next start takes up the current release of talgent")
	require.Equal(t, [2]string{newSlug, "2"}, [2]string{current[0].Slug, current[0].Version})

	rows, err := db.QueryContext(ctx, `SELECT user_id, slug, state, configuration FROM marketplace_installed_plugin WHERE slug <> 'other' ORDER BY user_id`)
	require.NoError(t, err)
	defer rows.Close()
	var migrated []string
	for rows.Next() {
		var user, slug, configuration string
		var state int
		require.NoError(t, rows.Scan(&user, &slug, &state, &configuration))
		migrated = append(migrated, fmt.Sprintf("%s %s %d %s", user, slug, state, configuration))
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{`alice talgent 1 {"token":"secret"}`, `bob talgent 2 {}`, `carol talgent 1 {"mode":"new"}`}, migrated)
}

func currentCatalog(slug, role string) command.MarketplacePort {
	return commandtest.StartCatalog(releaseCatalog{slug: slug, role: role, version: "2"}, creationConnections{})
}
