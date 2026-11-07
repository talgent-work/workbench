import { useState } from "react";
import { Button, Dropdown, Label, Tooltip } from "@heroui/react";
import type { InstalledPlugin, PluginSummary } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { AlertTriangle, ChevronRight, Lock, MoreHorizontal, Plus } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { useInstalledPlugins, usePluginActions, usePluginCatalog } from "../../lib/marketplace-catalog";
import { groupPluginsByCategory, isPlatformPlugin, pluginPresentation } from "../../lib/marketplace-plugins";
import { marketplacePaths } from "../../lib/marketplace-routes";
import { notify } from "../../lib/notifications";
import { installationNeedsSetup, missingRequired } from "../../lib/plugin-parameters";
import { PluginIcon } from "./marketplace-icons";
import { ConfirmDialog, LoadState, NothingMatches, SearchBar } from "./marketplace-shared";

const matches = (plugin: PluginSummary, query: string) => `${pluginPresentation(plugin).name} ${plugin.slug} ${plugin.description}`.toLowerCase().includes(query.trim().toLowerCase());

// Plugins grouped by purpose, "Other" last. A category page shows all of its
// Plugins; the overview shows the first six of each.
export function PluginList({ category }: { category?: string }) {
  const catalog = usePluginCatalog();
  const installed = useInstalledPlugins();
  const [query, setQuery] = useState("");
  const [removing, setRemoving] = useState<{ plugin: PluginSummary; installed: InstalledPlugin } | null>(null);
  const actions = usePluginActions();
  const groups = groupPluginsByCategory((catalog.data ?? []).filter(plugin => matches(plugin, query))).filter(group => !category || group.category === category);
  const uninstall = (target: { plugin: PluginSummary; installed: InstalledPlugin }) => {
    const name = pluginPresentation(target.plugin).name;
    actions.uninstall.mutate(target.installed, {
      onSuccess: () => notify(`plugin:${target.plugin.slug}`, "default", `${name} uninstalled`),
      onError: () => notify(`plugin:${target.plugin.slug}`, "danger", `${name} couldn’t be uninstalled`, "Refresh and try again."),
    });
  };
  return <>
    <SearchBar label={category ? `Search ${category.toLowerCase()} plugins` : "Search plugins"} value={query} onChange={setQuery} />
    <LoadState noun="plugins" isPending={catalog.isPending} isError={catalog.isError} hasData={Boolean(catalog.data)} isFetching={catalog.isFetching} onRetry={() => void catalog.refetch()} />
    {catalog.data && catalog.data.length === 0 && <div className="mk-empty"><p><strong>No plugins yet</strong></p><p className="mk-muted">Plugins appear here once the platform publishes them.</p></div>}
    {catalog.data && catalog.data.length > 0 && (groups.length === 0 ? <NothingMatches onClear={() => setQuery("")} />
      : groups.map(group => <section key={group.category} className="mk-section" aria-label={group.category}>
        {!category && <Link className="mk-section-head" to={marketplacePaths.pluginCategory(group.category)}>{group.category}<ChevronRight size={18} aria-hidden="true" /></Link>}
        <ul className="mk-rows">{(category ? group.plugins : group.plugins.slice(0, 6)).map(plugin => <PluginRow key={plugin.slug} plugin={plugin} installed={installed.data?.get(plugin.slug)} onUninstall={value => setRemoving({ plugin, installed: value })} />)}</ul>
      </section>))}
    <ConfirmDialog isOpen={removing !== null} onOpenChange={open => { if (!open) setRemoving(null); }} title={`Uninstall ${removing ? pluginPresentation(removing.plugin).name : ""}?`} confirmLabel="Uninstall" onConfirm={() => { if (removing) uninstall(removing); }}>
      <p>Your saved settings are removed. Works already running keep it. Connectors and connected Providers aren’t affected.</p>
    </ConfirmDialog>
  </>;
}

function PluginRow({ plugin, installed, onUninstall }: { plugin: PluginSummary; installed?: InstalledPlugin; onUninstall: (installed: InstalledPlugin) => void }) {
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
  const toggle = (value: InstalledPlugin) => actions.setEnabled.mutate({ installed: value, enabled: !value.enabled }, {
    onSuccess: () => notify(`plugin:${plugin.slug}`, "default", `${name} ${value.enabled ? "disabled" : "enabled"}`),
    onError: () => notify(`plugin:${plugin.slug}`, "danger", `${name} couldn’t be changed`, "Refresh and try again."),
  });
  return <li className="mk-row" data-off={installed ? !installed.enabled : undefined}>
    <Link className="mk-row-open" to={marketplacePaths.plugin(plugin.slug)}>
      <PluginIcon plugin={plugin} />
      <span className="mk-row-text"><strong>{name}{needsSetup && <AlertTriangle size={13} className="text-warning" aria-label="Needs attention" />}</strong><span>{installed && !installed.enabled ? "Disabled" : plugin.description}</span></span>
    </Link>
    {isPlatformPlugin(plugin.slug) ? <Tooltip><Tooltip.Trigger aria-label="Always enabled" className="mk-row-lock"><Lock size={16} /></Tooltip.Trigger><Tooltip.Content>Always enabled</Tooltip.Content></Tooltip>
      : installed ? <Dropdown><Button size="sm" variant="ghost" isIconOnly aria-label={`${name} options`}><MoreHorizontal size={18} /></Button>
        <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label={`${name} options`} onAction={key => {
          if (key === "open") navigate(marketplacePaths.plugin(plugin.slug));
          if (key === "toggle") toggle(installed);
          if (key === "uninstall") onUninstall(installed);
        }}>
          <Dropdown.Item id="open" textValue="Open"><Label>Open</Label></Dropdown.Item>
          <Dropdown.Item id="toggle" textValue={installed.enabled ? "Disable" : "Enable"}><Label>{installed.enabled ? "Disable" : "Enable"}</Label></Dropdown.Item>
          <Dropdown.Item id="uninstall" textValue="Uninstall" variant="danger"><Label>Uninstall…</Label></Dropdown.Item>
        </Dropdown.Menu></Dropdown.Popover></Dropdown>
        : <Button size="sm" variant="ghost" isIconOnly isPending={actions.install.isPending} aria-label={missingRequired(plugin.parameters, {}).length ? `Set up ${name}` : `Install ${name}`} onPress={install}><Plus size={20} strokeWidth={1.75} /></Button>}
  </li>;
}
