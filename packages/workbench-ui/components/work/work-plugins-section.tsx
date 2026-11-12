import { PluginPreparationStatus, type WorkPluginResult } from "@talgent/contracts/agent/public/v1/plugin_pb";
import { pluginDisplayName } from "../../lib/marketplace-plugins";
import { useWorkPluginResults } from "../../lib/work-plugin-results";
import { RailSection } from "../rail/rail-section";

const failed = (result: WorkPluginResult) => result.installation === PluginPreparationStatus.FAILED || result.loading === PluginPreparationStatus.FAILED;

// The version the Work's Harness loaded for each selected Plugin, which can be
// newer than the release the Work last took up. A Plugin not yet reported
// shows its name alone.
export function WorkPluginsSection({ workId }: { workId: string }) {
  const plugins = useWorkPluginResults(workId).data?.plugins ?? [];
  if (!plugins.length) return null;
  const loaded = plugins.filter(plugin => plugin.loading === PluginPreparationStatus.SUCCEEDED).length;
  return <RailSection title="Plugins" summary={loaded === plugins.length ? `${loaded} loaded` : `${loaded} of ${plugins.length} loaded`} defaultOpen={false}>
    <ul className="cr-plugins" aria-label="Work plugins">{plugins.map(plugin => <li key={plugin.slug}>
      <span title={plugin.slug}>{pluginDisplayName(plugin.slug)}</span>
      {failed(plugin) ? <span data-state="not-loaded">Not loaded</span>
        : plugin.loading === PluginPreparationStatus.SUCCEEDED && plugin.loadedVersion ? <span aria-label={`Version ${plugin.loadedVersion}`}>{plugin.loadedVersion}</span> : null}
    </li>)}</ul>
  </RailSection>;
}
