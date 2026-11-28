// PROTOTYPE ONLY: `pnpm prototype` serves the real app with an in-memory core.
import { fileURLToPath } from "node:url";
import { defineConfig, mergeConfig, type Plugin } from "vite";
import base from "../vite.config";

const fake = fileURLToPath(new URL("./fake-transport.ts", import.meta.url));

// Every import of workbench-ui's lib/transport, relative or by package path,
// resolves to the fake.
const fakeTransport: Plugin = {
  name: "prototype-fake-transport",
  enforce: "pre",
  async resolveId(source, importer, options) {
    if (!/(^|\/)transport(\.ts)?$/.test(source)) return null;
    const resolved = await this.resolve(source, importer, { ...options, skipSelf: true });
    return resolved?.id.endsWith("workbench-ui/lib/transport.ts") ? fake : null;
  },
};

export default mergeConfig(base, defineConfig({ root: fileURLToPath(new URL("..", import.meta.url)), plugins: [fakeTransport], server: { port: 3100, proxy: {} } }));
