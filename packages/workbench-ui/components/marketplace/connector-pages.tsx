import { useState } from "react";
import { Alert, Button, Card, Chip, Dropdown, Label, Switch, Typography } from "@heroui/react";
import { Code, ConnectError } from "@connectrpc/connect";
import type { Connector } from "@talgent/contracts/marketplace/public/v1/connectors_pb";
import { ChevronRight, Hammer, KeyRound, Lock, MoreHorizontal, Plus } from "lucide-react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { connectorLabel, installStatusLabel, pluginStatus, connectorStatus, publisherLabel, useConnector, useConnectorActions, useConnectors, useAlwaysEnabledPlugins, useInstalledPlugins, usePluginCatalog } from "../../lib/marketplace-catalog";
import { useConnectProvider, useProviderNames, useProviders } from "../../lib/marketplace-connections";
import { pluginDisplayName, pluginPresentation } from "../../lib/marketplace-plugins";
import { marketplacePaths, workshopPaths } from "../../lib/marketplace-routes";
import { notify } from "../../lib/notifications";
import { ConnectorImage } from "./connector-image";
import { ConnectorIcon, PluginIcon, ProviderIcon } from "./marketplace-icons";
import { ConfirmDialog, LoadState, NothingMatches, RowStatus, SearchBar } from "./marketplace-shared";

// The Marketplace finds and installs; it never edits. A person's own
// Connector links to its Workshop page instead.

// A Connector's sign-in always goes through its declared Provider; there is
// no manual credential entry.
function useConnectorCommands() {
  const actions = useConnectorActions();
  const connect = useConnectProvider();
  const providerName = useProviderNames();
  const { pathname } = useLocation();
  const install = (connector: Connector) => {
    const label = connectorLabel(connector);
    actions.install.mutate(connector.connectorId, {
      // Installation never waits for authorization; connecting is offered after.
      onSuccess: result => {
        const offer = result.provider && result.providerConnection !== "connected";
        notify(`connector:${connector.connectorId}`, "success", `Installed ${label}`, offer ? `Connect ${providerName(result.provider)} so Works can use it.` : "Works you start from now on include it.", {
          action: offer ? { label: `Connect ${providerName(result.provider)}`, onPress: () => connect.mutate({ provider: result.provider, returnTo: pathname }) } : { label: "Undo", onPress: () => actions.uninstall.mutate(result) },
        });
      },
      onError: () => notify(`connector:${connector.connectorId}`, "danger", `${label} couldn’t be installed`, "Try again."),
    });
  };
  const toggle = (connector: Connector) => {
    const enabled = connector.status !== "enabled";
    actions.setEnabled.mutate({ connector, enabled }, {
      onSuccess: () => notify(`connector:${connector.connectorId}`, "default", `${connectorLabel(connector)} turned ${enabled ? "on" : "off"}`),
      onError: () => notify(`connector:${connector.connectorId}`, "danger", `${connectorLabel(connector)} couldn’t be changed`, "Refresh and try again."),
    });
  };
  const uninstall = (connector: Connector) => actions.uninstall.mutate(connector, {
    onSuccess: () => notify(`connector:${connector.connectorId}`, "default", `${connectorLabel(connector)} uninstalled`),
    onError: () => notify(`connector:${connector.connectorId}`, "danger", `${connectorLabel(connector)} couldn’t be uninstalled`, "Refresh and try again."),
  });
  return { install, toggle, uninstall, connect, pending: actions.install.isPending };
}

function UninstallConnectorDialog({ connector, onClose, onConfirm }: { connector: Connector | null; onClose: () => void; onConfirm: (connector: Connector) => void }) {
  const providerName = useProviderNames();
  return <ConfirmDialog isOpen={connector !== null} onOpenChange={open => { if (!open) onClose(); }} title={`Uninstall ${connector ? connectorLabel(connector) : ""}?`} confirmLabel="Uninstall" onConfirm={() => { if (connector) onConfirm(connector); }}>
    <p>Works you start afterwards can’t use it. Works already running keep it.{connector?.provider ? ` Your ${providerName(connector.provider)} Provider stays connected.` : ""} Plugins aren’t affected.</p>
  </ConfirmDialog>;
}

/** Who published it: Talgent, another person, or you. */
function ByLine({ connector }: { connector: Connector }) {
  if (connector.publisher?.own && connector.state === "private") return <span className="flex items-center gap-1.5 text-[12px] text-muted"><Chip size="sm" variant="soft"><Lock size={11} aria-hidden="true" /><Chip.Label>Private</Chip.Label></Chip>Only you see it</span>;
  return <span className={`text-[12px] ${connector.publisher?.own ? "text-accent" : "text-muted"}`}>{publisherLabel(connector)}</span>;
}

export function ConnectorList({ category }: { category?: string }) {
  const connectors = useConnectors();
  const commands = useConnectorCommands();
  const [query, setQuery] = useState("");
  const rows = (connectors.data ?? []).filter(item => (!category || categoryOf(item) === category) && `${connectorLabel(item)} ${item.description} ${publisherLabel(item)}`.toLowerCase().includes(query.trim().toLowerCase()));
  const groups = [...new Set(rows.map(categoryOf))].sort((a, b) => a === "Other" ? 1 : b === "Other" ? -1 : a.localeCompare(b)).map(name => ({ name, items: rows.filter(item => categoryOf(item) === name) }));
  const row = (connector: Connector) => {
    const status = connectorStatus(connector), installed = status === "enabled" || status === "disabled";
    const label = connectorLabel(connector);
    return <li key={connector.connectorId} className="mk-row" data-off={status === "disabled" || undefined}>
      <Link className="mk-row-open" to={marketplacePaths.connector(connector.connectorId)}>
        <ConnectorImage connector={connector} />
        <span className="mk-row-text"><strong>{label}</strong><span>{connector.description}</span><ByLine connector={connector} /></span>
      </Link>
      {connector.required || installed ? <RowStatus required={connector.required} enabled={status === "enabled"} />
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
  </>;
}

const categoryOf = (connector: Connector) => connector.category ? pluginDisplayName(connector.category) : "Other";

/** What is left when a Connector is not in the person's Marketplace. */
function NotInMarketplace() {
  const navigate = useNavigate();
  return <Card className="mk-group"><div className="mk-empty"><strong>This isn’t in the Marketplace</strong><span className="text-muted">Its publisher withdrew it or never published it.</span>
    <Button size="sm" onPress={() => navigate(marketplacePaths.connectors)}>Browse connectors</Button></div></Card>;
}

export function ConnectorPage({ connectorId }: { connectorId: string }) {
  const detail = useConnector(connectorId);
  const commands = useConnectorCommands();
  const providerName = useProviderNames();
  const providers = useProviders();
  const catalog = usePluginCatalog();
  const installedPlugins = useInstalledPlugins();
  const required = useAlwaysEnabledPlugins();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [confirm, setConfirm] = useState(false);
  const connector = detail.data?.connector;
  if (!connector) return detail.isError && ConnectError.from(detail.error).code === Code.NotFound ? <NotInMarketplace />
    : <LoadState noun="connector" isPending={detail.isPending} isError={detail.isError} hasData={false} isFetching={detail.isFetching} onRetry={() => void detail.refetch()} />;
  const label = connectorLabel(connector), status = connectorStatus(connector), unconnected = Boolean(connector.provider) && connector.providerConnection !== "connected";
  const installed = status === "enabled" || status === "disabled";
  const own = Boolean(connector.publisher?.own);
  const provider = providers.data?.find(item => item.name === connector.provider);
  const releases = new Map((catalog.data ?? []).map(plugin => [plugin.slug, plugin]));
  const connect = () => commands.connect.mutate({ provider: connector.provider, returnTo: pathname });
  const connectLabel = connector.providerConnection === "reconnect_required" ? "Reconnect" : "Connect";
  return <div className="mk-detail">
    <header className="mk-detail-head">
      <ConnectorImage connector={connector} size={64} />
      <div className="mk-detail-title"><h2>{label}</h2><p className="mk-meta">{connector.category && <span>{categoryOf(connector)}</span>}<span>{own || connector.publisher?.kind === "platform" ? publisherLabel(connector) : `Published by ${publisherLabel(connector)}`}</span></p></div>
      <div className="mk-detail-actions">
        {own && <Button variant="ghost" onPress={() => navigate(workshopPaths.connector(connector.connectorId))}><Hammer size={15} aria-hidden="true" />Manage in workshop</Button>}
        {connector.required ? <span className="mk-muted mk-inline"><Lock size={15} aria-hidden="true" />Always on</span>
          : !installed ? connector.offered && <Button variant="primary" isPending={commands.pending} onPress={() => commands.install(connector)}>Install</Button>
          : <>
            <Switch isSelected={status === "enabled"} onChange={() => commands.toggle(connector)} aria-label={`Use ${label} in new Works`}><Switch.Control><Switch.Thumb /></Switch.Control></Switch>
            <Dropdown><Button size="sm" variant="ghost" isIconOnly aria-label={`More actions for ${label}`}><MoreHorizontal size={18} /></Button>
              <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label="More actions" onAction={key => { if (key === "uninstall") setConfirm(true); }}>
                <Dropdown.Item id="uninstall" textValue="Uninstall" variant="danger"><Label>Uninstall…</Label></Dropdown.Item>
              </Dropdown.Menu></Dropdown.Popover></Dropdown>
          </>}
      </div>
    </header>
    <UninstallConnectorDialog connector={confirm ? connector : null} onClose={() => setConfirm(false)} onConfirm={commands.uninstall} />
    {!connector.offered && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Title>Withdrawn</Alert.Title>
      <Alert.Description>Its publisher withdrew it. New Works leave it out; uninstall it when you no longer need it.</Alert.Description></Alert.Content></Alert>}
    {connector.offered && installed && unconnected && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Title>{connectLabel} {providerName(connector.provider)} to use {label}</Alert.Title>
      <Alert.Description>Your Works start without it until you sign in with your own {providerName(connector.provider)} account.</Alert.Description>
      <Button size="sm" variant="secondary" className="mt-2" isPending={commands.connect.isPending} onPress={connect}><KeyRound size={14} aria-hidden="true" />{connectLabel} {providerName(connector.provider)}</Button>
    </Alert.Content></Alert>}

    <section className="mk-section"><h3 className="mk-section-title">About</h3>
      <Card className="mk-group mk-about mk-about-single"><p className="mk-about-text">{connector.description || "No description."}</p></Card>
    </section>

    <section className="mk-section"><h3 className="mk-section-title">Sign-in</h3>
      <Card className="mk-group">{connector.provider ? <div className="flex items-center gap-3 p-4">
        <ProviderIcon id={connector.provider} size={32} /><span className="grid text-[13px]"><strong>Each person signs in with their own {providerName(connector.provider)} account</strong>
          <span className="text-muted">{connector.providerConnection === "connected" ? "You’re connected." : connector.providerConnection === "reconnect_required" ? "Your authorization expired." : "You haven’t connected yet."}
            {provider && !provider.targetCredentialsExpire ? ` Not available on your own computer: ${provider.displayName} access never expires.` : ""}</span></span>
      </div> : <Typography.Paragraph size="sm" className="p-4 text-muted">{connector.transport === "stdio" ? "Runs as a local command next to the Work and receives no credentials." : "Needs no sign-in."}</Typography.Paragraph>}</Card>
    </section>

    {(detail.data?.plugins.length ?? 0) > 0 && <section className="mk-section">
      <h3 className="mk-section-title">Plugins that use it <span className="mk-count">{detail.data!.plugins.length}</span></h3>
      <Card className="mk-group"><ul className="mk-group-list">{detail.data!.plugins.map(item => {
        const release = releases.get(item.slug);
        return <li key={item.slug} className="mk-group-link"><Link to={marketplacePaths.plugin(item.slug)}>
          {release ? <PluginIcon plugin={release} size={32} /> : <ConnectorIcon name={item.slug} size={32} />}
          <span className="mk-row-text"><strong>{release ? pluginPresentation(release).name : pluginDisplayName(item.slug)}<span className="mk-kind">Plugin</span></strong><span>{installStatusLabel[pluginStatus(required.isAlwaysEnabled(item.slug), installedPlugins.data?.get(item.slug))]}</span></span>
          <ChevronRight size={16} className="mk-muted ml-auto" aria-hidden="true" />
        </Link></li>;
      })}</ul></Card>
    </section>}
    {connector.publisher?.kind !== "platform" && !own && <Typography.Paragraph size="sm" className="text-muted">
      Published by {publisherLabel(connector)}{connector.publishedAtUnixMs > 0n ? ` on ${new Date(Number(connector.publishedAtUnixMs)).toLocaleDateString()}` : ""}. Talgent doesn’t review it; it runs with your own access only.
    </Typography.Paragraph>}
  </div>;
}
