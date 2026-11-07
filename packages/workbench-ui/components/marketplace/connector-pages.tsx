import { useState } from "react";
import { Alert, Button, Card, Dropdown, Label, Switch, Tooltip } from "@heroui/react";
import type { Connector } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { ChevronRight, KeyRound, Lock, MoreHorizontal, Plus } from "lucide-react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { installStatusLabel, pluginStatus, connectorStatus, useConnector, useConnectorActions, useConnectors, useInstalledPlugins, usePluginCatalog } from "../../lib/marketplace-catalog";
import { providerName, serviceLabel, useConnectProvider } from "../../lib/marketplace-connections";
import { isPlatformPlugin, pluginDisplayName, pluginPresentation } from "../../lib/marketplace-plugins";
import { marketplacePaths } from "../../lib/marketplace-routes";
import { notify } from "../../lib/notifications";
import { ConnectorIcon, PluginIcon, ProviderIcon } from "./marketplace-icons";
import { ConfirmDialog, LoadState, NothingMatches, SearchBar } from "./marketplace-shared";

// A Connector's sign-in always goes through its declared Provider; there is
// no manual credential entry.
function providerNotice(connector: Connector) {
  if (!connector.provider) return { text: "No sign-in needed", warning: false };
  const name = providerName(connector.provider);
  if (connector.providerConnection === "connected") return { text: `Signs in with your ${name} account`, warning: false };
  return { text: connector.providerConnection === "reconnect_required" ? `${name} authorization needs renewal` : `${name} isn’t connected yet`, warning: true };
}

function useConnectorCommands() {
  const actions = useConnectorActions();
  const connect = useConnectProvider();
  const { pathname } = useLocation();
  const install = (connector: Connector) => {
    const label = serviceLabel(connector.name);
    actions.install.mutate(connector.name, {
      // Installation never waits for authorization; connecting is offered after.
      onSuccess: result => {
        const offer = result.provider && result.providerConnection !== "connected";
        notify(`connector:${connector.name}`, "success", `${label} installed`, offer ? `Connect ${providerName(result.provider)} so Works can use it.` : "Works you start from now on can use it.", {
          action: offer ? { label: `Connect ${providerName(result.provider)}`, onPress: () => connect.mutate({ provider: result.provider, returnTo: pathname }) } : { label: "Undo", onPress: () => actions.uninstall.mutate(result) },
        });
      },
      onError: () => notify(`connector:${connector.name}`, "danger", `${label} couldn’t be installed`, "Try again."),
    });
  };
  const toggle = (connector: Connector) => {
    const enabled = connector.status !== "enabled";
    actions.setEnabled.mutate({ connector, enabled }, {
      onSuccess: () => notify(`connector:${connector.name}`, "default", `${serviceLabel(connector.name)} ${enabled ? "enabled" : "disabled"}`),
      onError: () => notify(`connector:${connector.name}`, "danger", `${serviceLabel(connector.name)} couldn’t be changed`, "Refresh and try again."),
    });
  };
  const uninstall = (connector: Connector) => actions.uninstall.mutate(connector, {
    onSuccess: () => notify(`connector:${connector.name}`, "default", `${serviceLabel(connector.name)} uninstalled`),
    onError: () => notify(`connector:${connector.name}`, "danger", `${serviceLabel(connector.name)} couldn’t be uninstalled`, "Refresh and try again."),
  });
  return { install, toggle, uninstall, connect, pending: actions.install.isPending };
}

function UninstallConnectorDialog({ connector, onClose, onConfirm }: { connector: Connector | null; onClose: () => void; onConfirm: (connector: Connector) => void }) {
  return <ConfirmDialog isOpen={connector !== null} onOpenChange={open => { if (!open) onClose(); }} title={`Uninstall ${connector ? serviceLabel(connector.name) : ""}?`} confirmLabel="Uninstall" onConfirm={() => { if (connector) onConfirm(connector); }}>
    <p>Works you start afterwards can’t use it. Works already running keep it.{connector?.provider ? ` Your ${providerName(connector.provider)} Provider stays connected.` : ""} Plugins aren’t affected.</p>
  </ConfirmDialog>;
}

export function ConnectorList({ category }: { category?: string }) {
  const connectors = useConnectors();
  const commands = useConnectorCommands();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [removing, setRemoving] = useState<Connector | null>(null);
  const rows = (connectors.data ?? []).filter(item => (!category || categoryOf(item) === category) && `${serviceLabel(item.name)} ${item.description}`.toLowerCase().includes(query.trim().toLowerCase()));
  const groups = [...new Set(rows.map(categoryOf))].sort((a, b) => a === "Other" ? 1 : b === "Other" ? -1 : a.localeCompare(b)).map(name => ({ name, items: rows.filter(item => categoryOf(item) === name) }));
  const row = (connector: Connector) => {
    const status = connectorStatus(connector), notice = providerNotice(connector), installed = status === "enabled" || status === "disabled";
    const label = serviceLabel(connector.name);
    return <li key={connector.name} className="mk-row" data-off={status === "disabled" || undefined}>
      <Link className="mk-row-open" to={marketplacePaths.connector(connector.name)}>
        <ConnectorIcon name={connector.name} />
        <span className="mk-row-text"><strong>{label}</strong><span className={installed && notice.warning ? "text-warning" : ""}>{status === "disabled" ? "Disabled" : installed && notice.warning ? notice.text : connector.description}</span></span>
      </Link>
      {connector.required ? <Tooltip><Tooltip.Trigger aria-label="Always enabled" className="mk-row-lock"><Lock size={16} /></Tooltip.Trigger><Tooltip.Content>Always enabled</Tooltip.Content></Tooltip>
        : installed ? <Dropdown><Button size="sm" variant="ghost" isIconOnly aria-label={`${label} options`}><MoreHorizontal size={18} /></Button>
          <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label={`${label} options`} onAction={key => {
            if (key === "open") navigate(marketplacePaths.connector(connector.name));
            if (key === "toggle") commands.toggle(connector);
            if (key === "uninstall") setRemoving(connector);
          }}>
            <Dropdown.Item id="open" textValue="Open"><Label>Open</Label></Dropdown.Item>
            <Dropdown.Item id="toggle" textValue={status === "enabled" ? "Disable" : "Enable"}><Label>{status === "enabled" ? "Disable" : "Enable"}</Label></Dropdown.Item>
            <Dropdown.Item id="uninstall" textValue="Uninstall" variant="danger"><Label>Uninstall…</Label></Dropdown.Item>
          </Dropdown.Menu></Dropdown.Popover></Dropdown>
          : <Button size="sm" variant="ghost" isIconOnly isPending={commands.pending} aria-label={`Install ${label}`} onPress={() => commands.install(connector)}><Plus size={20} strokeWidth={1.75} /></Button>}
    </li>;
  };
  return <>
    <SearchBar label={category ? `Search ${category.toLowerCase()} connectors` : "Search connectors"} value={query} onChange={setQuery} />
    <LoadState noun="connectors" isPending={connectors.isPending} isError={connectors.isError} hasData={Boolean(connectors.data)} isFetching={connectors.isFetching} onRetry={() => void connectors.refetch()} />
    {connectors.data && (groups.length === 0 ? (query ? <NothingMatches onClear={() => setQuery("")} /> : <div className="mk-empty"><p><strong>No connectors yet</strong></p></div>)
      : groups.map(group => <section key={group.name} className="mk-section" aria-label={group.name}>
        {!category && <Link className="mk-section-head" to={marketplacePaths.connectorCategory(group.name)}>{group.name}<ChevronRight size={18} aria-hidden="true" /></Link>}
        <ul className="mk-rows">{(category ? group.items : group.items.slice(0, 6)).map(row)}</ul>
      </section>))}
    <UninstallConnectorDialog connector={removing} onClose={() => setRemoving(null)} onConfirm={commands.uninstall} />
  </>;
}

const categoryOf = (connector: Connector) => connector.category ? pluginDisplayName(connector.category) : "Other";

export function ConnectorPage({ name }: { name: string }) {
  const detail = useConnector(name);
  const commands = useConnectorCommands();
  const catalog = usePluginCatalog();
  const installedPlugins = useInstalledPlugins();
  const { pathname } = useLocation();
  const [confirm, setConfirm] = useState(false);
  const connector = detail.data?.connector;
  if (!connector) return <LoadState noun="connector" isPending={detail.isPending} isError={detail.isError} hasData={false} isFetching={detail.isFetching} onRetry={() => void detail.refetch()} />;
  const label = serviceLabel(connector.name), status = connectorStatus(connector), notice = providerNotice(connector);
  const installed = status === "enabled" || status === "disabled";
  const releases = new Map((catalog.data ?? []).map(plugin => [plugin.slug, plugin]));
  const connect = () => commands.connect.mutate({ provider: connector.provider, returnTo: pathname });
  const connectLabel = connector.providerConnection === "reconnect_required" ? "Reconnect" : "Connect";
  return <div className="mk-detail">
    <header className="mk-detail-head">
      <ConnectorIcon name={connector.name} size={64} />
      <div className="mk-detail-title"><h2>{label}</h2><p className="mk-meta">{connector.category && <span>{categoryOf(connector)}</span>}<span>MCP connector</span></p></div>
      <div className="mk-detail-actions">
        {connector.required ? <span className="mk-muted mk-inline"><Lock size={15} aria-hidden="true" />Always enabled</span>
          : !installed ? <Button variant="primary" isPending={commands.pending} onPress={() => commands.install(connector)}>Install</Button>
          : <>
            <Switch isSelected={status === "enabled"} onChange={() => commands.toggle(connector)} aria-label={`Enable ${label}`}><Switch.Control><Switch.Thumb /></Switch.Control></Switch>
            <Dropdown><Button size="sm" variant="ghost" isIconOnly aria-label={`More actions for ${label}`}><MoreHorizontal size={18} /></Button>
              <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label="More actions" onAction={key => { if (key === "uninstall") setConfirm(true); }}>
                <Dropdown.Item id="uninstall" textValue="Uninstall" variant="danger"><Label>Uninstall…</Label></Dropdown.Item>
              </Dropdown.Menu></Dropdown.Popover></Dropdown>
          </>}
      </div>
    </header>
    <UninstallConnectorDialog connector={confirm ? connector : null} onClose={() => setConfirm(false)} onConfirm={commands.uninstall} />
    {installed && notice.warning && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Title>{notice.text}</Alert.Title>
      <Alert.Description>Works start without {label} access until you {connectLabel.toLowerCase()} your {providerName(connector.provider)} account.</Alert.Description>
      <Button size="sm" variant="secondary" className="mt-2" isPending={commands.connect.isPending} onPress={connect}><KeyRound size={14} />{connectLabel} {providerName(connector.provider)}</Button>
    </Alert.Content></Alert>}

    <section className="mk-section"><h3 className="mk-section-title">About</h3>
      <Card className="mk-group mk-about mk-about-single"><p className="mk-about-text">{connector.description}</p></Card>
    </section>

    {connector.provider && <section className="mk-section"><h3 className="mk-section-title">Provider</h3>
      <Card className="mk-group"><ul className="mk-group-list"><li className="mk-group-link mk-group-split">
        <Link to={marketplacePaths.provider(connector.provider)}>
          <ProviderIcon id={connector.provider} size={32} /><span className="mk-row-text"><strong>{providerName(connector.provider)}</strong><span className={notice.warning ? "text-warning" : ""}>{connector.providerConnection === "connected" ? "Connected" : connector.providerConnection === "reconnect_required" ? "Authorization expired" : "Not connected — required to use this connector"}</span></span>
          {!notice.warning && <ChevronRight size={16} className="mk-muted ml-auto" aria-hidden="true" />}
        </Link>
        {notice.warning && <Button size="sm" variant="secondary" isPending={commands.connect.isPending} onPress={connect}>{connectLabel}</Button>}
      </li></ul></Card>
    </section>}

    {(detail.data?.plugins.length ?? 0) > 0 && <section className="mk-section">
      <h3 className="mk-section-title">Plugins that use it <span className="mk-count">{detail.data!.plugins.length}</span></h3>
      <Card className="mk-group"><ul className="mk-group-list">{detail.data!.plugins.map(item => {
        const release = releases.get(item.slug);
        return <li key={item.slug} className="mk-group-link"><Link to={marketplacePaths.plugin(item.slug)}>
          {release ? <PluginIcon plugin={release} size={32} /> : <ConnectorIcon name={item.slug} size={32} />}
          <span className="mk-row-text"><strong>{release ? pluginPresentation(release).name : pluginDisplayName(item.slug)}<span className="mk-kind">Plugin</span></strong><span>{installStatusLabel[pluginStatus(isPlatformPlugin(item.slug), installedPlugins.data?.get(item.slug))]}</span></span>
          <ChevronRight size={16} className="mk-muted ml-auto" aria-hidden="true" />
        </Link></li>;
      })}</ul></Card>
    </section>}
  </div>;
}
