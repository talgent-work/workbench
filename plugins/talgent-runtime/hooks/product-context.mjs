import { readFileSync } from "node:fs";
import { join } from "node:path";

// The core product context every Work's session starts with. What a caller's
// Work adds, such as a Project's, comes from that caller's own plugin.
const plugin = process.env.CLAUDE_PLUGIN_ROOT || join(import.meta.dirname, "..");
process.stdout.write(readFileSync(join(plugin, "references", "product.md"), "utf8"));
