# workbench-ui

The Agent Workbench's pages, components, hooks, clients and styles. Every web app built on the
Workbench uses it; each app mounts the pages in its own shell through `EntryShellContext`
(`components/shell/entry-shell.ts`).

An app imports modules by path, such as `@talgent-work/workbench-ui/lib/client`, its stylesheet
right after Tailwind (`@import "@talgent-work/workbench-ui/styles/workbench.css"`), and its Vite
settings from `vite-preset.ts`: the public icons, bundle splitting, test environment and the
dev proxy to the core. Test support for apps is in `test-support/`.

The package is published as TypeScript source; the app's Vite and Tailwind compile it. Node does
not strip types from files under `node_modules`, so an app that installs the package from the
registry loads its Vite config with `--configLoader runner` (on `vite` and `vitest` alike).

The package depends only on the core.
