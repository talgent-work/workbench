import type { JsonObject } from "@bufbuild/protobuf";
import { queryOptions, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Connector, InstalledPlugin, PluginSummary } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { marketplaceClient } from "./client";
import { useCurrentUser } from "./account-service";
import { standaloneWorkStartSource } from "./standalone-work-service";

// Marketplace reads and an owner's changes. Catalog data is platform-wide; the
// owner is the signed-in person, or an Application they own when a call names
// its applicationId. Every owner-scoped query is keyed by the signed-in user
// and the Application so another session or Application never reuses it. A
// change also refreshes Start Work defaults and the Application's Work
// defaults, whose Startup Agents come from its Plugins.
export const marketplaceQueryKey = ["marketplace"] as const;
const pageSize = 100;

/** Whose installations a read or change reaches: the signed-in person's own,
 * or the Application they own named by applicationId. */
export type MarketplaceOwner = { applicationId?: string };

function usePersonalKey(owner: MarketplaceOwner, ...parts: string[]) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  return { key: [...marketplaceQueryKey, ...parts, userId, owner.applicationId ?? ""] as const, enabled: Boolean(userId) };
}

// The catalog has a few hundred Plugins; lists group and search all of them.
// A list carries summaries; a Plugin's full release is read by its page.
async function listAllPlugins(signal: AbortSignal) {
  const plugins: PluginSummary[] = [];
  for (let cursor = ""; ;) {
    const page = await marketplaceClient.listPlugins({ cursor, pageSize }, { signal });
    plugins.push(...page.plugins);
    if (!page.nextCursor) return plugins;
    cursor = page.nextCursor;
  }
}

// `enabled: false` defers a read until a surface needs it, such as the account
// menu, which loads what the person installed only once they reach for it.
type ReadOptions = MarketplaceOwner & { enabled?: boolean };

export function usePluginCatalog() {
  return useQuery({ queryKey: [...marketplaceQueryKey, "plugins"], queryFn: ({ signal }) => listAllPlugins(signal), retry: false, staleTime: 60_000 });
}

function pluginQuery(slug: string) {
  return queryOptions({ queryKey: [...marketplaceQueryKey, "plugin", slug], queryFn: async ({ signal }) => {
    const response = await marketplaceClient.getPlugin({ slug }, { signal });
    if (!response.plugin) throw new Error("The Plugin response is missing");
    return response.plugin;
  }, retry: false });
}

export function usePlugin(slug: string) {
  return useQuery(pluginQuery(slug));
}

/** The releases of a few Plugins by slug, sharing each Plugin page's cache. */
export function usePluginReleases(slugs: string[]) {
  const results = useQueries({ queries: slugs.map(slug => pluginQuery(slug)) });
  return new Map(results.flatMap((result, index) => result.data ? [[slugs[index], result.data] as const] : []));
}

export function useInstalledPlugins(options: ReadOptions = {}) {
  const { applicationId = "" } = options;
  const { key, enabled } = usePersonalKey(options, "installed-plugins");
  return useQuery({ queryKey: key, enabled: enabled && options.enabled !== false, retry: false, queryFn: async ({ signal }) => {
    const installed = new Map<string, InstalledPlugin>();
    for (let cursor = ""; ;) {
      const page = await marketplaceClient.listInstalledPlugins({ cursor, pageSize, applicationId }, { signal });
      for (const plugin of page.plugins) installed.set(plugin.slug, plugin);
      if (!page.nextCursor) return installed;
      cursor = page.nextCursor;
    }
  } });
}

// What every Work the person starts must load, the deployment's Built-in
// Plugins among them, is always enabled and cannot be installed or removed
// personally. The server's start options decide it, sharing their cache with
// the Start Work form; until they load nothing is known to be required.
export function useAlwaysEnabledPlugins() {
  const options = useQuery({ queryKey: ["work-start-options", ...standaloneWorkStartSource.key], queryFn: ({ signal }) => standaloneWorkStartSource.loadOptions(signal), retry: false });
  const required = new Set(options.data?.plugins.filter(plugin => plugin.required).map(plugin => plugin.name));
  return { isPending: options.isPending, isAlwaysEnabled: (slug: string) => required.has(slug) };
}

export function useConnectors(owner: MarketplaceOwner = {}) {
  const { key, enabled } = usePersonalKey(owner, "connectors");
  return useQuery({ queryKey: key, enabled, retry: false, queryFn: async ({ signal }) => (await marketplaceClient.listConnectors({ applicationId: owner.applicationId ?? "" }, { signal })).connectors });
}

export function useInstalledConnectors(options: ReadOptions = {}) {
  const { key, enabled } = usePersonalKey(options, "installed-connectors");
  return useQuery({ queryKey: key, enabled: enabled && options.enabled !== false, retry: false, queryFn: async ({ signal }) => (await marketplaceClient.listInstalledConnectors({ applicationId: options.applicationId ?? "" }, { signal })).connectors });
}

export function useConnector(name: string) {
  const { key, enabled } = usePersonalKey({}, "connector", name);
  return useQuery({ queryKey: key, enabled, retry: false, queryFn: ({ signal }) => marketplaceClient.getConnector({ name }, { signal }) });
}

function useRefreshPersonal() {
  const cache = useQueryClient();
  return () => Promise.all([
    cache.invalidateQueries({ queryKey: marketplaceQueryKey }),
    cache.invalidateQueries({ queryKey: ["work-start-options"] }),
    cache.invalidateQueries({ queryKey: ["application-agent"] }),
  ]);
}

export function usePluginActions(owner: MarketplaceOwner = {}) {
  const refresh = useRefreshPersonal();
  const applicationId = owner.applicationId ?? "";
  const install = useMutation({ mutationFn: async ({ slug, configuration }: { slug: string; configuration: JsonObject }) => {
    const response = await marketplaceClient.installPlugin({ slug, configuration, applicationId });
    if (response.plugin?.slug !== slug) throw new Error("The installation was not confirmed");
    return response.plugin;
  }, onSettled: refresh });
  const setEnabled = useMutation({ mutationFn: async ({ installed, enabled }: { installed: InstalledPlugin; enabled: boolean }) => {
    const request = { slug: installed.slug, version: installed.version, applicationId };
    await (enabled ? marketplaceClient.enablePlugin(request) : marketplaceClient.disablePlugin(request));
  }, onSettled: refresh });
  // Setting merges; settings the user cleared are deleted afterwards.
  const configure = useMutation({ mutationFn: async ({ installed, values, cleared = [] }: { installed: InstalledPlugin; values: JsonObject; cleared?: string[] }) => {
    let version = installed.version;
    if (Object.keys(values).length || !cleared.length) version = (await marketplaceClient.setPluginConfiguration({ slug: installed.slug, version, values, applicationId })).plugin?.version ?? version;
    if (cleared.length) await marketplaceClient.deletePluginConfiguration({ slug: installed.slug, version, names: cleared, applicationId });
  }, onSettled: refresh });
  const uninstall = useMutation({ mutationFn: (installed: InstalledPlugin) => marketplaceClient.uninstallPlugin({ slug: installed.slug, version: installed.version, applicationId }), onSettled: refresh });
  return { install, setEnabled, configure, uninstall };
}

export function useConnectorActions(owner: MarketplaceOwner = {}) {
  const refresh = useRefreshPersonal();
  const applicationId = owner.applicationId ?? "";
  const install = useMutation({ mutationFn: async (name: string) => {
    const response = await marketplaceClient.installConnector({ name, applicationId });
    if (response.connector?.name !== name) throw new Error("The installation was not confirmed");
    return response.connector;
  }, onSettled: refresh });
  const setEnabled = useMutation({ mutationFn: async ({ connector, enabled }: { connector: Connector; enabled: boolean }) => {
    const request = { name: connector.name, version: connector.installationVersion, applicationId };
    await (enabled ? marketplaceClient.enableConnector(request) : marketplaceClient.disableConnector(request));
  }, onSettled: refresh });
  const uninstall = useMutation({ mutationFn: (connector: Connector) => marketplaceClient.uninstallConnector({ name: connector.name, version: connector.installationVersion, applicationId }), onSettled: refresh });
  return { install, setEnabled, uninstall };
}

// Status vocabulary shared by Plugins and Connectors.
export type InstallStatus = "always_enabled" | "enabled" | "disabled" | "not_installed";
export const installStatusLabel: Record<InstallStatus, string> = { always_enabled: "Always enabled", enabled: "Enabled", disabled: "Disabled", not_installed: "Not installed" };

export function pluginStatus(required: boolean, installed?: InstalledPlugin): InstallStatus {
  if (required) return "always_enabled";
  if (!installed) return "not_installed";
  return installed.enabled ? "enabled" : "disabled";
}

export const connectorStatus = (connector: Connector) => connector.status as InstallStatus;
