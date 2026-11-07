import { availableParallelism } from "node:os";
import { fileURLToPath } from "node:url";
import type { ProxyOptions, UserConfig } from "vite";
import type { InlineConfig } from "vitest/node";

// What every web app built on workbench-ui shares: its public files, how its
// bundles split, its test environment and how its dev server reaches the core.

/** The brand and product icons the components reference by URL. */
export const publicDir = fileURLToPath(new URL("./public", import.meta.url));

export type ChunkGroup = { name: string; test: RegExp; priority: number };

// Libraries that load only with the lazy chunk that imports them, never in
// vendor: Shiki's languages and themes, and Mermaid with what only its diagrams
// use (cytoscape's layout-base and cose-base included). Merged into vendor they
// put megabytes on every first load. A name ending in "/" is a directory, any
// other a prefix.
const lazyLibraries = [
  "@shikijs/", "shiki/", "mermaid/", "@mermaid-js/", "@streamdown/mermaid/", "cytoscape", "layout-base/", "cose-base/", "d3", "dagre-d3-es/",
  "khroma/", "katex/", "roughjs/", "@iconify/", "@upsetjs/", "@braintree/", "delaunator/", "robust-predicates/", "internmap/", "langium/",
  "chevrotain", "lodash-es/", "dayjs/", "uuid/", "stylis/", "ts-dedent/", "marked/",
];

const separator = String.raw`[\\/]`;
const escaped = (name: string) => name.replace(/[.]/g, String.raw`\.`).replaceAll("/", separator);

/** How an app's bundles split: the core's groups and the app's own, and vendor
 * without the lazy libraries, the core's and the app's own. */
export function build(app: { groups?: ChunkGroup[]; lazy?: string[] } = {}): UserConfig["build"] {
  // This package splits like an app's own sources, also when installed from
  // the registry.
  const lazy = [".pnpm/", "@talgent/workbench-ui/", ...(app.lazy ?? []), ...lazyLibraries].map(escaped);
  return {
    rollupOptions: {
      output: {
        codeSplitting: {
          groups: [
            { name: "react-runtime", test: /node_modules[\\/](react|react-dom|scheduler)[\\/]/, priority: 40 },
            { name: "router-query", test: /node_modules[\\/](react-router|react-router-dom|@tanstack)[\\/]/, priority: 35 },
            { name: "connect-runtime", test: /node_modules[\\/](@bufbuild|@connectrpc|@talgent[\\/][\w-]+-contracts)[\\/]|gen[\\/]ts[\\/]dist[\\/]/, priority: 30 },
            {
              name: "markdown-highlight",
              test: /node_modules[\\/](react-markdown|react-syntax-highlighter|refractor|remark-gfm|unified|micromark|mdast-util-|hast-util-|property-information|vfile)[\\/]/,
              priority: 25,
            },
            ...(app.groups ?? []),
            { name: "vendor", test: new RegExp(`node_modules${separator}(?!${lazy.join("|")})`), priority: 1 },
          ],
        },
      },
    },
  };
}

export const test: InlineConfig = {
  globals: true,
  environment: "jsdom",
  setupFiles: fileURLToPath(new URL("./test-support/setup.ts", import.meta.url)),
  // A case takes at most 3s; one that needs longer is simulating a user, which
  // the browser verifies instead.
  testTimeout: 3_000,
  // Vitest defaults to one worker per CPU minus one, which runs serially on
  // the 2-vCPU CI runner; two workers there cut the suite by about 15%.
  maxWorkers: Math.max(2, availableParallelism() - 1),
};

/** The core's addresses on a local controlplane and Computer Gateway. */
export const coreProxy: Record<string, ProxyOptions> = {
  "/api": {
    target: "http://localhost:8080",
    changeOrigin: true,
    rewrite: path => path.replace(/^\/api/, ""),
  },
  // Account's sign-in endpoints.
  "/auth": {
    target: "http://localhost:8080",
    changeOrigin: true,
  },
  // The Computer Gateway's public routes; "/computers" is the web's own.
  "/computer/": {
    target: "http://localhost:8085",
    changeOrigin: true,
  },
};
