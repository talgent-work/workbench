import { create } from "@bufbuild/protobuf";
import { PluginSummarySchema, type PluginRelease } from "@talgent/workbench-contracts/marketplace/public/v1/marketplace_pb";

// ListPlugins lists summaries; fixtures describe full releases and list them through this.
export const summaryOf = (release: PluginRelease) => create(PluginSummarySchema, {
  slug: release.slug, source: release.source, version: release.version, description: release.description, nativeEntry: release.nativeEntry,
  skillCount: release.skills.length, agentCount: release.agents.length, mcpServerCount: release.mcpServers.length, parameters: release.parameters,
});
