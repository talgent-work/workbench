import type { PluginRelease } from "@talgent-work/workbench-contracts/marketplace/public/v1/marketplace_pb";

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

// The core runtime Plugin is required by every person's Agent definition; it
// is always enabled and cannot be installed or removed personally.
export const platformPluginSlugs: readonly string[] = ["talgent-runtime"];
export const isPlatformPlugin = (slug: string) => platformPluginSlugs.includes(slug);

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
