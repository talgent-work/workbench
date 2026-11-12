// PROTOTYPE (Applications): a sample Marketplace catalog and an Application's
// installations. Enabled installations are the default selection of every
// Work the Application starts; a start request can add or leave out any.
import { ModelVendor } from "@talgent/contracts/agent/public/v1/model_pb";
import type { PresentablePlugin } from "../../../lib/marketplace-plugins";
import type { StartModel } from "../../../lib/work-model";

export interface CatalogPlugin { slug: string; name: string; category: string; summary: string; skills: number; agents: number; mcp: number; agentNames?: string[]; parameter?: { name: string; label: string; description: string } }
export interface CatalogConnector { name: string; label: string; summary: string; provider?: string }

export const catalogPlugins: CatalogPlugin[] = [
  { slug: "ticket-triage", name: "Ticket triage", category: "productivity", summary: "Classify, prioritize and draft replies to support tickets.", skills: 3, agents: 1, mcp: 0, agentNames: ["triage-lead"] },
  { slug: "docs-answers", name: "Docs answers", category: "learning", summary: "Answer questions from your product documentation, citing the page.", skills: 2, agents: 0, mcp: 1, parameter: { name: "docsUrl", label: "Documentation URL", description: "Where your public docs live, such as https://docs.example.com." } },
  { slug: "repo-tools", name: "Repository tools", category: "development", summary: "Read failing builds, bisect changes and open small pull requests.", skills: 2, agents: 1, mcp: 0, agentNames: ["build-doctor"] },
  { slug: "security-review", name: "Security review", category: "security", summary: "Review a change for injection, secrets and unsafe defaults.", skills: 1, agents: 1, mcp: 0, agentNames: ["security-reviewer"] },
  { slug: "test-writer", name: "Test writer", category: "testing", summary: "Write regression tests that fail before a fix and pass after it.", skills: 2, agents: 0, mcp: 0 },
  { slug: "release-notes", name: "Release notes", category: "deployment", summary: "Draft release notes from merged pull requests.", skills: 1, agents: 0, mcp: 0 },
];

export const catalogConnectors: CatalogConnector[] = [
  { name: "linear", label: "Linear", summary: "Read and update issues and projects.", provider: "linear" },
  { name: "notion", label: "Notion", summary: "Search and edit pages and databases.", provider: "notion" },
  { name: "slack", label: "Slack", summary: "Read channels and post messages.", provider: "slack" },
  { name: "sentry", label: "Sentry", summary: "Look up errors, releases and stack traces.", provider: "sentry" },
  { name: "figma", label: "Figma", summary: "Read designs and comments.", provider: "figma" },
];

export const providerLabels: Record<string, string> = { github: "GitHub", linear: "Linear", notion: "Notion", slack: "Slack", sentry: "Sentry", figma: "Figma" };

export interface InstalledPlugin { slug: string; enabled: boolean; value?: string }
export interface InstalledConnector { name: string; enabled: boolean }

export const pluginOf = (slug: string) => catalogPlugins.find(plugin => plugin.slug === slug)!;
export const connectorOf = (name: string) => catalogConnectors.find(connector => connector.name === name)!;
export const needsSetup = (installed: InstalledPlugin) => Boolean(pluginOf(installed.slug).parameter && !installed.value);

/** What PluginIcon needs to draw a catalog Plugin's category tile. */
export const presentable = (plugin: CatalogPlugin): PresentablePlugin => ({ slug: plugin.slug, source: "", nativeEntry: new TextEncoder().encode(JSON.stringify({ displayName: plugin.name, category: plugin.category })) });

export function capabilityText(plugin: CatalogPlugin) {
  const parts = [[plugin.skills, "Skill"], [plugin.agents, "Agent"], [plugin.mcp, "MCP server"]] as const;
  return parts.filter(([count]) => count > 0).map(([count, noun]) => `${count} ${noun}${count === 1 ? "" : "s"}`).join(", ");
}

/** The deployment's Model Configurations, as the New work page offers them. */
export const sampleModels: StartModel[] = [
  { configurationId: "claude-sonnet", machineLogin: false, name: "Claude Sonnet 4.5", vendor: ModelVendor.ANTHROPIC, thinkingLevels: ["low", "medium", "high"], defaultThinkingLevel: "medium" },
  { configurationId: "claude-opus", machineLogin: false, name: "Claude Opus 4.1", vendor: ModelVendor.ANTHROPIC, thinkingLevels: ["low", "medium", "high", "xhigh", "max"], defaultThinkingLevel: "high" },
  { configurationId: "deepseek", machineLogin: false, name: "DeepSeek V3.2", vendor: ModelVendor.DEEPSEEK, thinkingLevels: ["low", "high"], defaultThinkingLevel: "high" },
  { configurationId: "kimi", machineLogin: false, name: "Kimi K2", vendor: ModelVendor.KIMI, thinkingLevels: [], defaultThinkingLevel: "" },
];

/** What a start request falls back to when it names none. */
export interface WorkDefaults { modelId: string; thinking: string; startupAgent: string }
