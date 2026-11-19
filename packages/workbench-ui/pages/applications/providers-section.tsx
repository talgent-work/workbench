import { useState } from "react";
import { Button, Card, Skeleton } from "@heroui/react";
import { useLocation } from "react-router-dom";
import { ProviderIcon } from "../../components/marketplace/marketplace-icons";
import { ConfirmDialog } from "../../components/marketplace/marketplace-shared";
import type { DeclaredProvider } from "@talgent/contracts/marketplace/public/v1/connections_pb";
import type { Application } from "../../lib/application-service";
import { useConnectProvider, useDisconnectProvider, useProviders } from "../../lib/marketplace-connections";
import { notify } from "../../lib/notifications";
import "../../components/marketplace/marketplace.css";

type Connection = "connected" | "not_connected" | "reconnect_required";
const connectionOf = (status?: string): Connection => status === "active" ? "connected" : status === "reconnect_required" ? "reconnect_required" : "not_connected";

/** The Application's own Provider Authorizations, one per declared Provider,
 * such as the GitHub account its Works check out repositories with. Its Application Owner connects them
 * through the Provider in the browser; they are never the owner's own. */
export function ProvidersSection({ application }: { application: Application }) {
  const providers = useProviders(application.id);
  return <section className="mk-section" aria-labelledby="ap-providers">
    <h3 id="ap-providers" className="mk-section-title">Providers</h3>
    <p className="mk-note">Accounts {application.name}’s Works act with, such as the GitHub repositories they check out. Connected for {application.name}, never for you.</p>
    {providers.isPending ? <Card className="mk-group" aria-busy="true"><Skeleton className="h-9 w-full rounded" /><p className="sr-only" role="status">Checking Providers…</p></Card>
      : !providers.data ? <Card className="mk-group"><div className="flex items-center gap-3"><span className="mk-row-text min-w-0 flex-1 text-warning">Providers unavailable</span>
        <Button size="sm" variant="secondary" isPending={providers.isFetching} onPress={() => void providers.refetch()}>Retry</Button></div></Card>
      : !providers.data.length ? <p className="mk-note">No Providers are available on this deployment.</p>
      : <Card className="mk-group">
        <ul className="mk-group-list" aria-label={`${application.name} Providers`}>
          {providers.data.map(provider => <ProviderRow key={provider.name} application={application} provider={provider} />)}
        </ul>
      </Card>}
  </section>;
}

function ProviderRow({ application, provider: declared }: { application: Application; provider: DeclaredProvider }) {
  const provider = declared.name;
  const connect = useConnectProvider();
  const disconnect = useDisconnectProvider(application.id);
  // The page, section and Application in view, so the callback returns here.
  const { pathname, search } = useLocation();
  const [confirm, setConfirm] = useState(false);
  const name = declared.displayName || provider;
  const current = declared.connection;
  const connection = connectionOf(current?.status);
  const start = () => connect.mutate({ provider, returnTo: pathname + search, applicationId: application.id });
  const confirmDisconnect = () => {
    if (!current?.authorizationId) return;
    disconnect.mutate(current.authorizationId, {
      onSuccess: () => notify(`provider:${provider}`, "default", `${name} disconnected from ${application.name}`, `Works ${application.name} starts afterwards can’t use it.`),
      onError: () => notify(`provider:${provider}`, "danger", `${name} couldn’t be disconnected`, `${name} didn’t confirm it. Try again.`),
    });
  };
  const status = connection === "connected" ? `Connected as ${current?.providerLogin}` : connection === "reconnect_required" ? "Authorization expired" : "Not connected";
  return <li className="!items-center">
    <ProviderIcon id={provider} size={36} />
    <span className="mk-row-text min-w-0 flex-1">
      <strong>{name}</strong>
      <span className={connection === "reconnect_required" ? "truncate text-warning" : "truncate"}>{status}</span>
    </span>
    {connection === "connected"
      ? <Button size="sm" variant="ghost" className="text-danger" isPending={disconnect.isPending} onPress={() => setConfirm(true)}>Disconnect</Button>
      : <Button size="sm" variant="secondary" isPending={connect.isPending} onPress={start}>{connection === "reconnect_required" ? "Reconnect" : "Connect"}</Button>}
    <ConfirmDialog isOpen={confirm} onOpenChange={setConfirm} title={`Disconnect ${name} from ${application.name}?`} confirmLabel="Disconnect" onConfirm={confirmDisconnect}>
      <p>Works {application.name} starts afterwards can’t use {name} until you connect it again. Works already running keep their access.</p>
    </ConfirmDialog>
  </li>;
}
