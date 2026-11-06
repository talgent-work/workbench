import { useState } from "react";
import { Alert, Button, Card, Link as HeroLink, Spinner } from "@heroui/react";
import type { ProviderAccount } from "@talgent-work/workbench-contracts/marketplace/public/v1/connections_pb";
import { Check, ChevronRight, ExternalLink, RefreshCw } from "lucide-react";
import { Link, useLocation } from "react-router-dom";
import { connectorStatus, installStatusLabel, useConnectors } from "../../lib/marketplace-catalog";
import { providerName, serviceLabel, supportedProviders, useConnectProvider, useDisconnectProvider, useProviderAccess, useProviderConnection, useRefreshProviderAccess } from "../../lib/marketplace-connections";
import { marketplacePaths } from "../../lib/marketplace-routes";
import { notify } from "../../lib/notifications";
import { timeAgo } from "../../lib/relative-time";
import { ConnectorIcon, ProviderIcon } from "./marketplace-icons";
import { ConfirmDialog, LoadState } from "./marketplace-shared";

const descriptions: Record<string, string> = { github: "Repositories, issues and pull requests on GitHub." };
type Connection = "connected" | "not_connected" | "reconnect_required";
const connectionOf = (status?: string): Connection => status === "active" ? "connected" : status === "reconnect_required" ? "reconnect_required" : "not_connected";
const connectionText: Record<Connection, string> = { connected: "Connected", not_connected: "Not connected", reconnect_required: "Authorization expired" };

// Every supported Provider with the person's Provider Authorization state; a
// row opens the Provider page. Profile lists them.
export function ProviderRows() {
  return <ul className="mk-rows">{supportedProviders.map(provider => <ProviderRow key={provider} provider={provider} />)}</ul>;
}

// A row shows the stored status only; who is connected and what the
// authorization reaches are shown on its page.
function ProviderRow({ provider }: { provider: string }) {
  const access = useProviderConnection(provider);
  const connection = connectionOf(access.data?.state?.status);
  return <li className="mk-row"><Link className="mk-row-open" to={marketplacePaths.provider(provider)}>
    <ProviderIcon id={provider} /><span className="mk-row-text"><strong>{providerName(provider)}</strong>
      <span className={connection === "reconnect_required" || (access.isError && !access.data) ? "text-warning" : ""}>{access.isPending ? "Checking…" : access.isError && !access.data ? "Connection status unavailable" : connectionText[connection]}</span></span>
  </Link></li>;
}

// A Provider page: connecting never installs anything and applies to Works
// started afterwards. What the authorization reaches is decided on GitHub and
// shown as last seen; Refresh asks GitHub again.
export function ProviderPage({ provider }: { provider: string }) {
  const access = useProviderAccess(provider);
  const refresh = useRefreshProviderAccess(provider);
  const connectors = useConnectors();
  const connect = useConnectProvider();
  const disconnect = useDisconnectProvider();
  const { pathname } = useLocation();
  const [confirm, setConfirm] = useState(false);
  const name = providerName(provider);
  if (!access.data) return <LoadState noun={`${name} connection`} isPending={access.isPending} isError={access.isError} hasData={false} isFetching={access.isFetching} onRetry={() => void access.refetch()} />;
  const state = access.data.state;
  const connection = connectionOf(state?.status);
  const observed = access.data.observedAtUnixMs ? new Date(Number(access.data.observedAtUnixMs)) : null;
  const used = (connectors.data ?? []).filter(item => item.provider === provider);
  const affected = used.filter(item => connectorStatus(item) !== "not_installed");
  const start = () => connect.mutate({ provider, returnTo: pathname });
  const confirmDisconnect = () => {
    if (!state?.authorizationId) return;
    disconnect.mutate(state.authorizationId, {
      onSuccess: () => notify(`provider:${provider}`, "default", `${name} disconnected`, "Works you start afterwards can’t use it."),
      onError: () => notify(`provider:${provider}`, "danger", `${name} couldn’t be disconnected`, "GitHub didn’t confirm it. Try again."),
    });
  };
  return <div className="mk-detail">
    <header className="mk-detail-head">
      <ProviderIcon id={provider} size={64} />
      <div className="mk-detail-title"><h2>{name}</h2><p className="mk-meta"><span>Provider</span><span className={connection === "reconnect_required" ? "text-warning" : ""}>{connectionText[connection]}</span></p></div>
      <div className="mk-detail-actions">
        {connection === "connected" ? <Button variant="ghost" className="text-danger" isPending={disconnect.isPending} onPress={() => setConfirm(true)}>Disconnect</Button>
          : <Button variant="primary" isPending={connect.isPending} onPress={start}>{connection === "reconnect_required" ? `Reconnect ${name}` : `Connect ${name}`}</Button>}
      </div>
    </header>
    <ConfirmDialog isOpen={confirm} onOpenChange={setConfirm} title={`Disconnect ${name}?`} confirmLabel="Disconnect" onConfirm={confirmDisconnect}>
      <p>Works you start afterwards can’t use {name} until you connect again. Works already running keep their access.</p>
      {affected.length > 0 && <><p className="mt-2">These connectors stay installed but need you to connect again:</p><ul className="mt-1 list-disc pl-5">{affected.map(item => <li key={item.name}>{serviceLabel(item.name)}</li>)}</ul></>}
    </ConfirmDialog>
    {connection === "reconnect_required" && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Title>Your {name} authorization expired</Alert.Title><Alert.Description>New Works start without {name} access until you reconnect. Running Works aren’t changed.</Alert.Description></Alert.Content></Alert>}
    {access.data.accessUnavailable && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Title>{name} couldn’t be checked</Alert.Title><Alert.Description>Showing what it allowed when last seen. Your connection is unchanged.</Alert.Description>
      <Button size="sm" variant="secondary" className="mt-2" isPending={refresh.isPending} onPress={() => refresh.mutate()}>Try again</Button></Alert.Content></Alert>}

    <section className="mk-section"><h3 className="mk-section-title">About</h3>
      <Card className="mk-group mk-about">
        <div className="mk-about-main"><p className="mk-about-text">{descriptions[provider]} Only you can use this authorization. Works you start after connecting can use it; Works already running keep the access they started with.</p></div>
        <dl className="mk-about-facts">
          <div><dt>Account</dt><dd>{connection === "not_connected" ? "—" : state?.providerLogin}</dd></div>
          <div><dt>Status</dt><dd>{connectionText[connection]}</dd></div>
        </dl>
      </Card>
    </section>

    {access.data.permissions.length > 0 && <section className="mk-section">
      <h3 className="mk-section-title">{connection === "connected" ? "Permissions" : "Talgent will ask to"}</h3>
      <Card className="mk-group"><ul className="mk-group-list">{access.data.permissions.map(permission => <li key={permission.name}>
        <Check size={16} className="mk-muted" aria-hidden="true" /><span className="mk-row-text"><strong>{serviceLabel(permission.name)}</strong><span>{permission.access === "write" ? "Read and write" : permission.access === "admin" ? "Admin" : "Read only"}</span></span>
      </li>)}</ul></Card>
    </section>}

    {connection === "connected" && <section className="mk-section">
      <div className="mk-section-bar"><h3 className="mk-section-title">Organizations <span className="mk-count">{access.data.accounts.length}</span></h3>
        <span className="mk-section-actions">
          <span className="mk-note">{observed ? `Checked ${timeAgo(observed)}` : "Not checked yet"}</span>
          <Button size="sm" variant="ghost" isPending={refresh.isPending} onPress={() => refresh.mutate()}>{({ isPending }) => <>{isPending ? <Spinner color="current" size="sm" /> : <RefreshCw size={14} aria-hidden="true" />}Refresh</>}</Button>
          {access.data.manageUrl && <HeroLink href={access.data.manageUrl} target="_blank" rel="noreferrer" className="mk-manage">Manage on {name}<ExternalLink size={13} aria-hidden="true" /></HeroLink>}
        </span></div>
      {access.data.accounts.length ? <Card className="mk-group"><ul className="mk-group-list">{access.data.accounts.map(account => <li key={account.login}>
        <span className="mk-row-text"><strong>{account.login}</strong><span>{scope(account)}</span></span>
        {account.settingsUrl && <HeroLink href={account.settingsUrl} target="_blank" rel="noreferrer" className="mk-manage ml-auto" aria-label={`Change access for ${account.login} on ${name}`}><ExternalLink size={13} /></HeroLink>}
      </li>)}</ul></Card> : <p className="mk-note">{observed ? `${name} doesn’t allow Talgent into any account yet. Add accounts and repositories on ${name}.` : `Refresh to see which accounts ${name} allows.`}</p>}
      <p className="mk-note">Works can check out the repositories these accounts allow. Change which ones on {name}.</p>
    </section>}

    {used.length > 0 && <section className="mk-section">
      <h3 className="mk-section-title">Connectors that use it <span className="mk-count">{used.length}</span></h3>
      <Card className="mk-group"><ul className="mk-group-list">{used.map(item => <li key={item.name} className="mk-group-link"><Link to={marketplacePaths.connector(item.name)}>
        <ConnectorIcon name={item.name} size={32} /><span className="mk-row-text"><strong>{serviceLabel(item.name)}<span className="mk-kind">Connector</span></strong><span>{installStatusLabel[connectorStatus(item)]}</span></span>
        <ChevronRight size={16} className="mk-muted ml-auto" aria-hidden="true" />
      </Link></li>)}</ul></Card>
    </section>}
  </div>;
}

const scope = (account: ProviderAccount) => `${account.type === "Organization" ? "Organization" : "Personal account"} · ${account.repositorySelection === "all" ? "All repositories" : `${account.repositories.length} selected ${account.repositories.length === 1 ? "repository" : "repositories"}`}`;
