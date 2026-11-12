import type { PluginRelease, PluginSummary } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";

/** What presenting a Plugin needs; a full release and a catalog summary both carry it. */
export type PresentablePlugin = Pick<PluginRelease, "slug" | "source" | "nativeEntry">;

export function pluginDisplayName(slug: string): string {
  return slug.split("-").map(word => word.charAt(0).toUpperCase() + word.slice(1)).join(" ");
}

function nativeObject(bytes: Uint8Array): Record<string, unknown> | undefined {
  try {
    const value: unknown = JSON.parse(new TextDecoder().decode(bytes));
    return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
  } catch { return undefined; }
}

const text = (value: unknown) => typeof value === "string" && value.trim() ? value.trim() : "";

// Presentation comes from the release's native marketplace entry.
export function pluginPresentation(plugin: PresentablePlugin) {
  const entry = nativeObject(plugin.nativeEntry);
  const author = entry?.author;
  const keywords = Array.isArray(entry?.keywords) ? entry.keywords : Array.isArray(entry?.tags) ? entry.tags : [];
  const homepage = text(entry?.homepage) || text(entry?.repository);
  return {
    name: text(entry?.displayName) || pluginDisplayName(plugin.slug),
    category: text(entry?.category) ? pluginDisplayName(text(entry?.category)) : "Other",
    author: text(author) || (author && typeof author === "object" ? text((author as Record<string, unknown>).name) : ""),
    license: text(entry?.license),
    homepage: /^https:\/\//.test(homepage) ? homepage : "",
    keywords: keywords.filter((word): word is string => typeof word === "string" && Boolean(word.trim())),
  };
}

export function groupPluginsByCategory<T extends PresentablePlugin>(plugins: T[]) {
  const groups = new Map<string, T[]>();
  for (const plugin of plugins) {
    const { category } = pluginPresentation(plugin);
    const entries = groups.get(category) ?? [];
    entries.push(plugin);
    groups.set(category, entries);
  }
  return [...groups].sort(([left], [right]) => {
    if (left === "Other") return 1;
    if (right === "Other") return -1;
    return left.localeCompare(right);
  }).map(([category, entries]) => ({ category, plugins: entries }));
}

/** What a Plugin brings, as counts of its Skills, Agents and MCP servers. */
export function capabilityText(plugin: Pick<PluginSummary, "skillCount" | "agentCount" | "mcpServerCount">) {
  const parts = [[plugin.skillCount, "Skill"], [plugin.agentCount, "Agent"], [plugin.mcpServerCount, "MCP server"]] as const;
  return parts.filter(([count]) => count > 0).map(([count, noun]) => `${count} ${noun}${count === 1 ? "" : "s"}`).join(", ");
}
