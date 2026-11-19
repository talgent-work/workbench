import { useMutation, useQuery } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";
import type { Connector, ConnectorIcon } from "@talgent/contracts/marketplace/public/v1/connectors_pb";
import type { DeclaredProvider } from "@talgent/contracts/marketplace/public/v1/connections_pb";
import { useCurrentUser } from "./account-service";
import { marketplaceClient } from "./client";
import { marketplaceQueryKey, useRefreshPersonal, type MarketplaceOwner } from "./marketplace-catalog";

// The Workshop: what an owner makes and publishes. Adding never installs;
// installing stays in the Marketplace.

/** An icon image as the person chose it, before or after it is saved. */
export type IconImage = { mediaType: string; data: Uint8Array; url: string };

export type ConnectorDraft = {
  name: string;
  transport: "http" | "stdio";
  url: string;
  /** The command line: the command and its arguments, split on spaces. */
  command: string;
  provider: string;
  description: string;
  category: string;
  icon?: IconImage;
};

export const emptyDraft: ConnectorDraft = { name: "", transport: "http", url: "", command: "", provider: "", description: "", category: "" };

export type DraftProblems = Partial<Record<keyof ConnectorDraft, string>>;

export const maxIconBytes = 512 * 1024;
export const iconTypes = ["image/png", "image/svg+xml", "image/jpeg", "image/webp"];

/** The command line of a local command Connector. */
export const commandLine = (connector: Connector) => [connector.settings?.command ?? "", ...(connector.settings?.arguments ?? [])].filter(Boolean).join(" ");

export function draftOf(connector: Connector): ConnectorDraft {
  return { name: connector.name, transport: connector.transport === "stdio" ? "stdio" : "http", url: connector.settings?.url ?? "", command: commandLine(connector),
    provider: connector.provider, description: connector.description, category: connector.category };
}

const hostOf = (address: string) => { try { return new URL(address).hostname; } catch { return ""; } };

/** Whether an address lies on one of a Provider's target hosts, as the
 * Marketplace decides it: HTTPS on the default port. */
export function withinTargets(address: string, provider?: DeclaredProvider) {
  let url: URL;
  try { url = new URL(address); } catch { return false; }
  if (!provider || url.protocol !== "https:" || (url.port && url.port !== "443")) return false;
  const host = hostOf(address);
  return provider.targets.some(target => target.hosts.some(pattern => pattern.startsWith("*.") ? host.endsWith(pattern.slice(1)) && host !== pattern.slice(2) : host === pattern));
}

export const providerHosts = (provider?: DeclaredProvider) => provider?.targets.flatMap(target => target.hosts) ?? [];

/** The checks the Marketplace makes when a Connector is added or changed,
 * shown inline before the request; the Marketplace still decides. */
export function draftProblems(draft: ConnectorDraft, options: { adding: boolean; providers: DeclaredProvider[]; takenNames: string[] }): DraftProblems {
  const problems: DraftProblems = {};
  if (options.adding) {
    if (!/^[a-z0-9][a-z0-9-]*$/.test(draft.name) || draft.name.length > 128) problems.name = "Use lowercase letters, digits and hyphens.";
    else if (options.takenNames.includes(draft.name)) problems.name = "You already have a connector with this name.";
  }
  const provider = options.providers.find(item => item.name === draft.provider);
  if (draft.transport === "stdio") {
    if (!draft.command.trim()) problems.command = "Enter the command that starts the server.";
    if (draft.provider) problems.provider = "A connector that signs in through a provider is a remote server.";
  } else if (!/^https?:\/\/[^/]/.test(draft.url.trim())) problems.url = "Enter the server’s http(s) address.";
  else if (provider && !withinTargets(draft.url.trim(), provider)) problems.url = `${provider.displayName} credentials only reach ${providerHosts(provider).join(", ")}.`;
  if (draft.description.length > 1024) problems.description = "Keep it under 1,024 characters.";
  if (draft.category.length > 64) problems.category = "Keep it under 64 characters.";
  return problems;
}

/** A command line as its command and arguments. */
export function splitCommand(line: string) {
  const [command = "", ...args] = line.trim().split(/\s+/).filter(Boolean);
  return { command, arguments: args };
}

function settingsOf(draft: ConnectorDraft) {
  return draft.transport === "http" ? { url: draft.url.trim(), command: "", arguments: [] } : { url: "", ...splitCommand(draft.command) };
}

const iconMessage = (icon?: IconImage): ConnectorIcon | undefined => icon ? { $typeName: "marketplace.public.v1.ConnectorIcon", mediaType: icon.mediaType, data: icon.data } : undefined;

/** A refusal the Marketplace explained, such as a taken name. */
export const refusal = (error: unknown) => ConnectError.from(error).rawMessage.replace(/^marketplace: /, "");

/** The field a refused addition points at, by the refusal's code: a taken or
 * reserved name, or a connection the Marketplace does not accept. */
export function refusedField(error: unknown): keyof ConnectorDraft | undefined {
  const code = ConnectError.from(error).code;
  return code === Code.AlreadyExists ? "name" : code === Code.InvalidArgument ? "url" : undefined;
}

// Icons are shown as data URLs, which need no release when they are dropped.
const dataUrl = (mediaType: string, data: Uint8Array) => {
  let binary = "";
  for (let at = 0; at < data.length; at += 0x8000) binary += String.fromCharCode(...data.subarray(at, at + 0x8000));
  return `data:${mediaType};base64,${btoa(binary)}`;
};

function useOwnerKey(owner: MarketplaceOwner, ...parts: string[]) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  return { key: [...marketplaceQueryKey, ...parts, userId, owner.applicationId ?? ""] as const, enabled: Boolean(userId) };
}

/** Every Connector the owner made, Withdrawn ones included. */
export function useOwnedConnectors(owner: MarketplaceOwner = {}) {
  const { key, enabled } = useOwnerKey(owner, "owned-connectors");
  return useQuery({ queryKey: key, enabled, retry: false, queryFn: async ({ signal }) => (await marketplaceClient.listOwnedConnectors({ applicationId: owner.applicationId ?? "" }, { signal })).connectors });
}

/** A Connector's icon image as a data URL, cached by its digest. */
export function useConnectorIcon(connector: Pick<Connector, "connectorId" | "iconDigest">, owner: MarketplaceOwner = {}) {
  return useQuery({
    queryKey: [...marketplaceQueryKey, "connector-icon", connector.connectorId, connector.iconDigest],
    enabled: Boolean(connector.iconDigest), retry: false, staleTime: Infinity, gcTime: 10 * 60_000,
    queryFn: async ({ signal }) => {
      const icon = (await marketplaceClient.getConnectorIcon({ connectorId: connector.connectorId, applicationId: owner.applicationId ?? "" }, { signal })).icon;
      return icon ? dataUrl(icon.mediaType, icon.data) : "";
    },
  });
}

/** The owner's commands on their own Connectors. Each refreshes the
 * Marketplace, the Workshop and Start Work. */
export function useConnectorAuthoring(owner: MarketplaceOwner = {}) {
  const refresh = useRefreshPersonal();
  const applicationId = owner.applicationId ?? "";
  const confirmed = (connector?: Connector) => {
    if (!connector) throw new Error("The change was not confirmed");
    return connector;
  };
  const add = useMutation({ mutationFn: async (draft: ConnectorDraft) => confirmed((await marketplaceClient.addConnector({
    applicationId, name: draft.name, transport: draft.transport, provider: draft.transport === "http" ? draft.provider : "",
    settings: settingsOf(draft), description: draft.description.trim(), category: draft.category.trim(), icon: iconMessage(draft.icon),
  })).connector), onSettled: refresh });
  const change = useMutation({ mutationFn: async ({ connector, draft }: { connector: Connector; draft: ConnectorDraft }) => confirmed((await marketplaceClient.changeConnector({
    applicationId, connectorId: connector.connectorId, version: connector.connectorVersion, settings: settingsOf(draft), description: draft.description.trim(), category: draft.category.trim(),
  })).connector), onSettled: refresh });
  const changeIcon = useMutation({ mutationFn: async ({ connector, icon }: { connector: Connector; icon?: IconImage }) => confirmed((await marketplaceClient.changeConnectorIcon({
    applicationId, connectorId: connector.connectorId, version: connector.connectorVersion, icon: iconMessage(icon),
  })).connector), onSettled: refresh });
  const command = (connector: Connector) => ({ applicationId, connectorId: connector.connectorId, version: connector.connectorVersion });
  const publish = useMutation({ mutationFn: async (connector: Connector) => confirmed((await marketplaceClient.publishConnector(command(connector))).connector), onSettled: refresh });
  const withdraw = useMutation({ mutationFn: async (connector: Connector) => confirmed((await marketplaceClient.withdrawConnector(command(connector))).connector), onSettled: refresh });
  const remove = useMutation({ mutationFn: async (connector: Connector) => { await marketplaceClient.deleteConnector(command(connector)); }, onSettled: refresh });
  return { add, change, changeIcon, publish, withdraw, remove };
}

/** Reads a chosen image file as an icon, or says why it can't be one. */
export async function readIcon(file: File): Promise<IconImage | string> {
  if (!iconTypes.includes(file.type)) return "Use a PNG, SVG, JPG or WebP image.";
  if (file.size > maxIconBytes) return "Use an image under 512 KB.";
  const data = new Uint8Array(await file.arrayBuffer());
  return { mediaType: file.type, data, url: dataUrl(file.type, data) };
}
