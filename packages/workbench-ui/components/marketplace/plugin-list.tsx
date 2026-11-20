import { useState } from "react";
import { Button } from "@heroui/react";
import type { InstalledPlugin, PluginSummary } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { AlertTriangle, ChevronRight, Plus } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { useAlwaysEnabledPlugins, useInstalledPlugins, usePluginActions, usePluginCatalog } from "../../lib/marketplace-catalog";
import { groupPluginsByCategory, pluginPresentation } from "../../lib/marketplace-plugins";
import { marketplacePaths } from "../../lib/marketplace-routes";
import { notify } from "../../lib/notifications";
import { installationNeedsSetup, missingRequired } from "../../lib/plugin-parameters";
import { PluginIcon } from "./marketplace-icons";
import { LoadState, NothingMatches, RowStatus, SearchBar } from "./marketplace-shared";

const matches = (plugin: PluginSummary, query: string) => `${pluginPresentation(plugin).name} ${plugin.slug} ${plugin.description}`.toLowerCase().includes(query.trim().toLowerCase());

// Plugins grouped by purpose, "Other" last. A category page shows all of its
// Plugins; the overview shows the first six of each.
export function PluginList({ category }: { category?: string }) {
  const catalog = usePluginCatalog();
  const installed = useInstalledPlugins();
  const required = useAlwaysEnabledPlugins();
  const [query, setQuery] = useState("");
  const groups = groupPluginsByCategory((catalog.data ?? []).filter(plugin => matches(plugin, query))).filter(group => !category || group.category === category);
  return <>
    <SearchBar label={category ? `Search ${category.toLowerCase()} plugins` : "Search plugins"} value={query} onChange={setQuery} />
    <LoadState noun="plugins" isPending={catalog.isPending} isError={catalog.isError} hasData={Boolean(catalog.data)} isFetching={catalog.isFetching} onRetry={() => void catalog.refetch()} />
    {catalog.data && catalog.data.length === 0 && <div className="mk-empty"><p><strong>No plugins yet</strong></p><p className="mk-muted">Plugins appear here once the platform publishes them.</p></div>}
    {catalog.data && catalog.data.length > 0 && (groups.length === 0 ? <NothingMatches onClear={() => setQuery("")} />
      : groups.map(group => <section key={group.category} className="mk-section" aria-label={group.category}>
        {!category && <Link className="mk-section-head" to={marketplacePaths.pluginCategory(group.category)}>{group.category}<ChevronRight size={18} aria-hidden="true" /></Link>}
        <ul className="mk-rows">{(category ? group.plugins : group.plugins.slice(0, 6)).map(plugin => <PluginRow key={plugin.slug} plugin={plugin} alwaysEnabled={required.isAlwaysEnabled(plugin.slug)} requiredPending={required.isPending} installed={installed.data?.get(plugin.slug)} />)}</ul>
      </section>))}
  </>;
}

// An installed or always-on Plugin's row shows its status; it is turned off,
// on or uninstalled on its page.
function PluginRow({ plugin, alwaysEnabled, requiredPending, installed }: { plugin: PluginSummary; alwaysEnabled: boolean; requiredPending: boolean; installed?: InstalledPlugin }) {
  const navigate = useNavigate();
  const actions = usePluginActions();
  const { name } = pluginPresentation(plugin);
  const needsSetup = installed ? installationNeedsSetup(plugin.parameters, installed) : false;
  const install = () => {
    // Required settings are entered on the Plugin page, never half-installed.
    if (missingRequired(plugin.parameters, {}).length) { navigate(marketplacePaths.plugin(plugin.slug)); return; }
    actions.install.mutate({ slug: plugin.slug, configuration: {} }, {
      onSuccess: result => notify(`plugin:${plugin.slug}`, "success", `${name} installed`, "Works you start from now on include it.", {
        action: { label: "Undo", onPress: () => actions.uninstall.mutate(result, { onSuccess: () => notify(`plugin:${plugin.slug}`, "default", `${name} uninstalled`) }) },
      }),
      onError: () => notify(`plugin:${plugin.slug}`, "danger", `${name} couldn’t be installed`, "Try again from its page."),
    });
  };
  return <li className="mk-row" data-off={installed ? !installed.enabled : undefined}>
    <Link className="mk-row-open" to={marketplacePaths.plugin(plugin.slug)}>
      <PluginIcon plugin={plugin} />
      <span className="mk-row-text"><strong>{name}{needsSetup && <AlertTriangle size={13} className="text-warning" aria-label="Needs attention" />}</strong><span>{plugin.description}</span></span>
    </Link>
    {alwaysEnabled || installed ? <RowStatus required={alwaysEnabled} enabled={Boolean(installed?.enabled)} />
      : <Button size="sm" variant="ghost" isIconOnly isPending={actions.install.isPending} isDisabled={requiredPending} aria-label={missingRequired(plugin.parameters, {}).length ? `Set up ${name}` : `Install ${name}`} onPress={install}><Plus size={20} strokeWidth={1.75} /></Button>}
  </li>;
}
