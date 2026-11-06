# Talgent Workbench Web

Talgent Workbench's web app, served on `workbench.<domain>`.
`src/workbench-routes.tsx` registers its routes: New work, a Work, Library, Usage,
Marketplace, Profile and Computers, all pages of [`workbench-ui`](../../packages/workbench-ui)
in the Workbench's own shell.

## Local development

From the repository root, install the workspace with `pnpm install --frozen-lockfile` and
generate the contracts with `make generate`, then run
`pnpm --filter @talgent/workbench-web dev` and open `http://localhost:3000`. The dev server
proxies `/api` and `/auth` to a local `controlplane` on port 8080 and `/computer/` to a local
Computer Gateway on port 8085. To sign in locally, register a GitHub OAuth App whose callback
URL is `http://localhost:3000/auth/github/callback` and give the controlplane its client id
and secret (`ACCOUNT_GITHUB_CLIENT_ID`, `ACCOUNT_GITHUB_CLIENT_SECRET`) and a master key file
(`ACCOUNT_MASTER_KEY_PATH`).

Checks are the Makefile's `frontend-typecheck`, `frontend-test`, `frontend-build` and
`frontend-lint` targets.
