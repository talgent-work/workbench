import type { JsonObject } from "@bufbuild/protobuf";
import type { WorkStartConnector, WorkStartPlugin } from "@talgent-work/workbench-contracts/agent/public/v1/work_start_options_pb";
import type { Connector, InstalledPlugin, PluginRelease, PluginSummary } from "@talgent-work/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { pluginDisplayName, pluginPresentation } from "./marketplace-plugins";
import { missingRequired, parseParameters, parameterLabel } from "./plugin-parameters";
import type { WorkStartDraft } from "./work-start-draft";

export type Difference = "required" | "default" | "added" | "removed";
export const differenceLabel: Record<Difference, string> = { required: "Always included", default: "Your default", added: "Added for this Work", removed: "Removed for this Work" };

export interface Capability { name: string; description: string; argumentHint: string; plugin: string }
export interface LoadoutRow {
  slug: string;
  name: string;
  difference: Difference;
  included: boolean;
  release?: PluginSummary;
  installed?: InstalledPlugin;
  skills: Capability[];
  agents: Capability[];
  /** Settings changed for this Work only. */
  edited: string[];
  /** A blocking problem, such as a required setting nobody provided. */
  problem: string;
}

// The Plugins of this Work: required ones, the starter's enabled defaults and
// per-Work additions, each marked by how it differs from the defaults. Agent
// reports the Skills and Agents of the defaults; an addition's come from its
// full release (`details`), since the catalog lists summaries.
export function pluginLoadout(options: WorkStartPlugin[], catalog: PluginSummary[], installed: Map<string, InstalledPlugin> | undefined, draft: WorkStartDraft, details: Map<string, PluginRelease> = new Map()): LoadoutRow[] {
  const releases = new Map(catalog.map(release => [release.slug, release]));
  const rows = new Map<string, LoadoutRow>();
  const row = (slug: string, base: "required" | "default" | "added", option?: WorkStartPlugin): LoadoutRow => {
    const release = releases.get(slug), choice = draft.plugins[slug], mine = installed?.get(slug);
    const included = base === "required" || (base === "added" ? true : choice?.include !== false);
    const name = release ? pluginPresentation(release).name : option?.displayName && option.displayName !== slug ? option.displayName : pluginDisplayName(slug);
    const capability = (items: Array<{ name: string; description: string; argumentHint?: string }>) => items.map(item => ({ name: item.name, description: item.description, argumentHint: item.argumentHint ?? "", plugin: name }));
    const edited = Object.entries(choice?.values ?? {}).filter(([, value]) => value.trim()).map(([key]) => key);
    // Saved settings are unknown until the installations load; nothing is flagged before.
    const missing = release && base !== "required" && installed ? missingRequired(release.parameters, { configuration: mine?.configuration, redacted: mine?.redactedParameters }, choice?.values) : [];
    return {
      slug, name, release, installed: mine, included, edited,
      difference: base === "default" && !included ? "removed" : base,
      skills: capability(option?.skills ?? details.get(slug)?.skills ?? []), agents: capability(option?.agents ?? details.get(slug)?.agents ?? []),
      problem: included && missing.length ? `Needs ${missing.map(key => parameterLabel(key, release!.parameters[key]).toLowerCase()).join(", ")}` : "",
    };
  };
  for (const option of options) rows.set(option.name, row(option.name, option.required ? "required" : "default", option));
  for (const [slug, choice] of Object.entries(draft.plugins)) if (choice.include && !rows.has(slug)) rows.set(slug, row(slug, "added"));
  return [...rows.values()];
}

// Explicit per-Work differences sent at creation. Parameter overrides are
// validated against the release; they never change personal settings.
export function pluginChoices(rows: LoadoutRow[], draft: WorkStartDraft) {
  const plugins: Array<{ name: string; include: boolean; configuration: JsonObject }> = [];
  const errors: Record<string, Record<string, string>> = {};
  for (const row of rows) {
    if (row.difference === "required") continue;
    const choice = draft.plugins[row.slug];
    if (!row.included) { plugins.push({ name: row.slug, include: false, configuration: {} }); continue; }
    if (row.difference === "default" && !row.edited.length) continue;
    const parsed = row.release ? parseParameters(row.release.parameters, choice?.values ?? {}, { configuration: row.installed?.configuration, redacted: row.installed?.redactedParameters }) : { values: {}, errors: {} };
    if (Object.keys(parsed.errors).length) errors[row.slug] = parsed.errors;
    plugins.push({ name: row.slug, include: true, configuration: parsed.values });
  }
  return { plugins, errors };
}

export interface ConnectorRow {
  name: string;
  difference: Difference;
  included: boolean;
  /** Marketplace presentation and Provider connection, when listed. */
  connector?: Connector;
  /** Left out of a Work on the chosen Managed Computer, as Agent reports;
   * never included or sent. */
  unavailable: boolean;
}

// Agent reports the default selection (required entries and enabled
// installations); this Work may remove defaults or add other offered entries.
// On a Managed Computer, Agent says which Connectors its Work leaves out.
export function connectorLoadout(options: WorkStartConnector[], catalog: Connector[], draft: WorkStartDraft, managed = false): ConnectorRow[] {
  const presentation = new Map(catalog.map(connector => [connector.name, connector]));
  return options.map(option => {
    const base: Difference = option.required ? "required" : option.includedByDefault ? "default" : "added";
    const connector = presentation.get(option.name);
    const unavailable = managed && option.unavailableOnManaged;
    const choice = draft.connectors[option.name];
    const included = !unavailable && (base === "required" || (choice ?? base === "default"));
    return { name: option.name, connector, included, unavailable, difference: base === "default" && !included && !unavailable ? "removed" : base };
  });
}

export function connectorChoices(rows: ConnectorRow[]) {
  return rows.filter(row => !row.unavailable && (row.difference === "removed" || (row.difference === "added" && row.included)))
    .map(row => ({ name: row.name, include: row.included }));
}
