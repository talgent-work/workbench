import { Navigate, Route, Routes, useParams } from "react-router-dom";
import { ConnectorList, ConnectorPage } from "../components/marketplace/connector-pages";
import { LoadState } from "../components/marketplace/marketplace-shared";
import { MarketplaceShell } from "../components/marketplace/marketplace-shell";
import { PluginList } from "../components/marketplace/plugin-list";
import { PluginPage } from "../components/marketplace/plugin-page";
import { ProviderPage } from "../components/marketplace/provider-pages";
import { usePlugin } from "../lib/marketplace-catalog";
import { providerName, serviceLabel } from "../lib/marketplace-connections";
import { pluginDisplayName, pluginPresentation } from "../lib/marketplace-plugins";
import { marketplacePaths } from "../lib/marketplace-routes";

// Marketplace › Section › Item. Every object is a page; the topbar trail
// carries the levels above the current page, and the topbar switches the
// catalog between Plugins and Connectors.
const plugins = { label: "Plugins", to: marketplacePaths.plugins };
const connectors = { label: "Connectors", to: marketplacePaths.connectors };
// Provider Authorizations are about the person, so their pages sit under Profile.
const profile = { label: "Profile", to: "/profile" };

export function MarketplacePage() {
  return <Routes>
    <Route index element={<MarketplaceShell title="Plugins"><PluginList /></MarketplaceShell>} />
    <Route path="plugins" element={<Navigate to={marketplacePaths.plugins} replace />} />
    <Route path="plugins/categories/:category" element={<CategoryRoute kind="plugins" />} />
    <Route path="plugins/:slug" element={<PluginRoute />} />
    <Route path="connectors" element={<MarketplaceShell title="Connectors"><ConnectorList /></MarketplaceShell>} />
    <Route path="connectors/categories/:category" element={<CategoryRoute kind="connectors" />} />
    <Route path="connectors/:name" element={<ConnectorRoute />} />
    <Route path="providers" element={<Navigate to="/profile" replace />} />
    <Route path="providers/:provider" element={<ProviderRoute />} />
    <Route path="*" element={<Navigate to={marketplacePaths.plugins} replace />} />
  </Routes>;
}

function CategoryRoute({ kind }: { kind: "plugins" | "connectors" }) {
  const category = useParams().category ?? "";
  return <MarketplaceShell title={category} trail={[kind === "plugins" ? plugins : connectors]}>
    {kind === "plugins" ? <PluginList category={category} /> : <ConnectorList category={category} />}
  </MarketplaceShell>;
}

function PluginRoute() {
  const slug = useParams().slug ?? "";
  const plugin = usePlugin(slug);
  return <MarketplaceShell title={plugin.data ? pluginPresentation(plugin.data).name : pluginDisplayName(slug)} trail={[plugins]}>
    {plugin.data ? <PluginPage key={slug} plugin={plugin.data} /> : <LoadState noun="plugin" isPending={plugin.isPending} isError={plugin.isError} hasData={false} isFetching={plugin.isFetching} onRetry={() => void plugin.refetch()} />}
  </MarketplaceShell>;
}

function ConnectorRoute() {
  const name = useParams().name ?? "";
  return <MarketplaceShell title={serviceLabel(name)} trail={[connectors]}><ConnectorPage key={name} name={name} /></MarketplaceShell>;
}

function ProviderRoute() {
  const provider = useParams().provider ?? "";
  return <MarketplaceShell title={providerName(provider)} root={profile}><ProviderPage key={provider} provider={provider} /></MarketplaceShell>;
}
