import type { JsonObject } from "@bufbuild/protobuf";
import { queryOptions, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Connector, InstalledPlugin, PluginSummary } from "@talgent-work/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { marketplaceClient } from "./client";
import { useCurrentUser } from "./account-service";

// Marketplace reads and personal changes. Catalog data is platform-wide; every
// personal query is keyed by the signed-in user so another session never
// reuses it. A personal change also refreshes Start Work defaults.
export const marketplaceQueryKey = ["marketplace"] as const;
const pageSize = 100;

function usePersonalKey(...parts: string[]) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  return { key: [...marketplaceQueryKey, ...parts, userId] as const, enabled: Boolean(userId) };
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
type ReadOptions = { enabled?: boolean };

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
  const { key, enabled } = usePersonalKey("installed-plugins");
  return useQuery({ queryKey: key, enabled: enabled && options.enabled !== false, retry: false, queryFn: async ({ signal }) => {
    const installed = new Map<string, InstalledPlugin>();
    for (let cursor = ""; ;) {
      const page = await marketplaceClient.listInstalledPlugins({ cursor, pageSize }, { signal });
      for (const plugin of page.plugins) installed.set(plugin.slug, plugin);
      if (!page.nextCursor) return installed;
      cursor = page.nextCursor;
    }
  } });
}

export function useConnectors() {
  const { key, enabled } = usePersonalKey("connectors");
  return useQuery({ queryKey: key, enabled, retry: false, queryFn: async ({ signal }) => (await marketplaceClient.listConnectors({}, { signal })).connectors });
}

export function useInstalledConnectors(options: ReadOptions = {}) {
  const { key, enabled } = usePersonalKey("installed-connectors");
  return useQuery({ queryKey: key, enabled: enabled && options.enabled !== false, retry: false, queryFn: async ({ signal }) => (await marketplaceClient.listInstalledConnectors({}, { signal })).connectors });
}

export function useConnector(name: string) {
  const { key, enabled } = usePersonalKey("connector", name);
  return useQuery({ queryKey: key, enabled, retry: false, queryFn: ({ signal }) => marketplaceClient.getConnector({ name }, { signal }) });
}

function useRefreshPersonal() {
  const cache = useQueryClient();
  return () => Promise.all([
    cache.invalidateQueries({ queryKey: marketplaceQueryKey }),
    cache.invalidateQueries({ queryKey: ["work-start-options"] }),
  ]);
}

export function usePluginActions() {
  const refresh = useRefreshPersonal();
  const install = useMutation({ mutationFn: async ({ slug, configuration }: { slug: string; configuration: JsonObject }) => {
    const response = await marketplaceClient.installPlugin({ slug, configuration });
    if (response.plugin?.slug !== slug) throw new Error("The installation was not confirmed");
    return response.plugin;
  }, onSettled: refresh });
  const setEnabled = useMutation({ mutationFn: async ({ installed, enabled }: { installed: InstalledPlugin; enabled: boolean }) => {
    const request = { slug: installed.slug, version: installed.version };
    await (enabled ? marketplaceClient.enablePlugin(request) : marketplaceClient.disablePlugin(request));
  }, onSettled: refresh });
  // Setting merges; settings the user cleared are deleted afterwards.
  const configure = useMutation({ mutationFn: async ({ installed, values, cleared = [] }: { installed: InstalledPlugin; values: JsonObject; cleared?: string[] }) => {
    let version = installed.version;
    if (Object.keys(values).length || !cleared.length) version = (await marketplaceClient.setPluginConfiguration({ slug: installed.slug, version, values })).plugin?.version ?? version;
    if (cleared.length) await marketplaceClient.deletePluginConfiguration({ slug: installed.slug, version, names: cleared });
  }, onSettled: refresh });
  const uninstall = useMutation({ mutationFn: (installed: InstalledPlugin) => marketplaceClient.uninstallPlugin({ slug: installed.slug, version: installed.version }), onSettled: refresh });
  return { install, setEnabled, configure, uninstall };
}

export function useConnectorActions() {
  const refresh = useRefreshPersonal();
  const install = useMutation({ mutationFn: async (name: string) => {
    const response = await marketplaceClient.installConnector({ name });
    if (response.connector?.name !== name) throw new Error("The installation was not confirmed");
    return response.connector;
  }, onSettled: refresh });
  const setEnabled = useMutation({ mutationFn: async ({ connector, enabled }: { connector: Connector; enabled: boolean }) => {
    const request = { name: connector.name, version: connector.installationVersion };
    await (enabled ? marketplaceClient.enableConnector(request) : marketplaceClient.disableConnector(request));
  }, onSettled: refresh });
  const uninstall = useMutation({ mutationFn: (connector: Connector) => marketplaceClient.uninstallConnector({ name: connector.name, version: connector.installationVersion }), onSettled: refresh });
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
