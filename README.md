<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.png">
  <img alt="Talgent Workbench" src="docs/assets/logo-light.png" width="420">
</picture>

### Your own Agent, ready for real work.

A managed agent platform you run yourself. Every Work in its own sandbox or on your own machine,
with the model you choose.

[![Release](https://img.shields.io/github/v/release/talgent-work/workbench?sort=semver&color=6a4cf5)](https://github.com/talgent-work/workbench/releases/latest)
[![License](https://img.shields.io/github/license/talgent-work/workbench?color=blue)](LICENSE)
[![npm @talgent/computer](https://img.shields.io/npm/v/@talgent/computer?label=%40talgent%2Fcomputer&logo=npm)](https://www.npmjs.com/package/@talgent/computer)

**[Live demo](https://workbench.talgent.work)** · [Website](https://talgent.work) · [Get started](#get-started) · [Documentation](#documentation) ·
[Releases](https://github.com/talgent-work/workbench/releases) · [Report a bug](https://github.com/talgent-work/workbench/issues/new/choose)

<img alt="A Work in Talgent Workbench: the conversation with the Agent, its progress, Artifacts and token usage" src="docs/assets/work.webp" width="900">

</div>

Talgent Workbench gives every person, and every program you register, an AI Agent of their own.
Start a Work, hand it files, follow what it does as it happens and collect the results.

Workbench runs Claude Code natively and hosts everything around it: a sandbox per Work, sessions
that resume where they stopped, credentials kept out of reach, Plugins, files, sharing and usage.
All of it, including the conversations and telemetry, runs in your own Kubernetes cluster.

## Why Workbench

- **The whole platform is yours.** The control plane, the Agents' conversations, their files and
  their telemetry live in your cluster, not only the sandboxes. Apache-2.0, so you can read and
  audit the code that runs your Agents.
- **Any model, per Work.** Offer Claude, DeepSeek, Qwen, Kimi, GLM, MiniMax or any vendor with an
  Anthropic-compatible API, each with its own key. People and programs choose one when they start
  a Work.
- **In the cloud or on your own machine.** Run a Work in a VM-isolated sandbox on your cluster, or
  on your own Mac, Linux or Windows computer. The conversation, Library, Usage and Plugins are the
  same either way.
- **For people and for programs.** A web app where everyone has their own Agent, and the same
  public APIs for the Applications you register, each with its own Agent, Plugins and
  authorizations.
- **The Claude Code ecosystem as it is.** Plugins come from standard Claude Code plugin catalogs,
  so the Skills and Agents you already use work unchanged.
- **Costs and access you can trace.** Token usage by person, Work, model and Application. Every
  Work, file and share belongs to the person or Application that made it.

## Two ways to run a Work

Each time you start a Work, choose where it runs. Everything else stays the same.

| | Cloud (hosted) | Managed Computer |
|---|---|---|
| **Where it runs** | In a fresh sandbox on your Workbench's Kubernetes cluster | On your own Mac, Linux or Windows machine, connected with Talgent Computer |
| **Best for** | Work that should run any time, with nothing to set up and no machine left on | Work that needs the files, tools and apps already on your machine |
| **Isolation** | Its own VM-isolated sandbox, cut off from your private network; credentials and model keys never enter it | You choose how much the Agent may do there, and can confine it to the operating system's sandbox on macOS and Linux |
| **Setup** | None for you; the deployment provides it | Install `@talgent/computer` from npm and connect the machine from your Computers page. It needs only an outbound connection and keeps itself up to date |

## What you can do

**Hand work to your Agent.** Describe the task, attach files, pick a repository, a model and the
Plugins it needs. Steer it while it runs, stop it, and pick it up later where it left off.

**Watch it work.** The conversation streams live: the Agent's thinking, the tools it calls, its
plan and its progress. Share a Work with colleagues so they can follow it read-only until you
revoke access.

**Keep everything it made.** Reports, pages, spreadsheets and decks the Agent publishes land in
your Library, next to what others have shared with you. Office documents, PDFs and images preview
in the browser, and a Share Link hands a file to anyone you choose.

**Extend it from the Marketplace.** Add the Plugins (Skills and specialist Agents) and Connectors
your Workbench offers, and authorize your own GitHub account for the repositories a Work checks out.

**See what it costs.** Token use over time, by kind and by model, and the Works that used the most.

<table>
  <tr>
    <td width="50%"><img alt="New work: an instruction, a repository, the model and the Plugins the Work loads" src="docs/assets/new-work.webp"><p align="center"><sub>Start a Work with what it needs</sub></p></td>
    <td width="50%"><img alt="Library: Artifacts of a person's Works and of the Works shared with them" src="docs/assets/library.webp"><p align="center"><sub>Everything your Agents made, in your Library</sub></p></td>
  </tr>
  <tr>
    <td width="50%"><img alt="Usage: token use over time by kind and by model" src="docs/assets/usage.webp"><p align="center"><sub>Usage down to the token</sub></p></td>
    <td width="50%"><img alt="Marketplace: Plugins by category" src="docs/assets/marketplace.webp"><p align="center"><sub>Plugins and Connectors from the Marketplace</sub></p></td>
  </tr>
</table>

<sub>Screenshots show sample data.</sub>

## Build on Workbench with Applications

Register an **Application** and your own systems or product drive Agents through the same public
APIs the web app uses: start Works, follow them, collect their Artifacts and read their usage. Each
Application has its own Agent, Plugins and authorizations, signs in with OAuth 2.0 client
credentials, labels its Works to attribute usage to its own users, and can give a Work its own MCP
tools.

## From every person to the whole company: Talgent Orchestra

Workbench gives every person an Agent of their own. When work outgrows one person, **Talgent
Orchestra**, built on Workbench, brings those Agents together across a company's projects, so
every change reaches the people it affects and finished work becomes company knowledge.

<div align="center">
<img alt="Every goal of a project on one board, each with its owner, priority and status" src="docs/assets/goal-board.webp" width="900">
</div>

- **Goals with an owner.** Every goal of a project on one board, with its owner, its priority and
  the Agents working on it.
- **A coordinator that keeps every Agent in step.** It knows how the goals depend on each other.
  When one changes, the owners of the others hear about it, and their Agents adjust.
- **Decisions you can trace.** Every decision the coordinator makes is on a timeline, with what it
  rested on, what it did and how that went.
- **Knowledge that compounds.** Finished work becomes pages the whole project learns from, every
  statement traced to the work it came from.

Say Marketing's Agent moves checkout to show prices in EUR. The coordinator sees that invoices and
the Q4 report depend on it, and asks Billing and Finance. Billing needs two more days, so the launch
moves by two days and every owner is told. The EU pricing rules are kept for the next time.

<table>
  <tr>
    <td width="50%"><img alt="A coordinator's decision on the timeline, opened to show its inputs, its actions and each action's outcome" src="docs/assets/decision-chain.webp"><p align="center"><sub>Every decision, with its inputs and outcomes</sub></p></td>
    <td width="50%"><img alt="What a project knows: its pages and the finished work they came from" src="docs/assets/company-knowledge.webp"><p align="center"><sub>What the project has learned, and from where</sub></p></td>
  </tr>
</table>

<div align="center">

**[Discover Talgent Orchestra at talgent.work →](https://talgent.work)**

</div>

## Get started

- **Try it now** in the [live demo](https://workbench.talgent.work): sign in with GitHub and start a
  Work.
- **Deploy Workbench** on your Kubernetes cluster with the [self-hosting guide](docs/self-hosting.md).
- **Connect your machine** with Talgent Computer: install `@talgent/computer` from npm and follow
  the Computers page of your Workbench (macOS, Linux and Windows).
- **Talk to us** about Talgent at [talgent.work](https://talgent.work).

## Documentation

| Topic | Where |
|---|---|
| Deploying on Kubernetes | [Self-hosting guide](docs/self-hosting.md) |
| Every setting of every process | [Configuration reference](docs/configuration.md) |
| What an upgrade asks of an operator | [Release notes](docs/release-notes.md) |
| Processes, stores and repository layout | [Architecture](docs/architecture.md) |
| Building, testing and releases | [Development](docs/development.md) |
| Talgent Computer and its gateway | [Talgent Computer protocol](docs/computer-protocol.md) |
| Calling Workbench as an Application: tokens, APIs, identity | [Contracts repository](https://github.com/talgent-work/contracts): `docs/application-tokens.md`, `proto/*/public` |

## Contributing

Report bugs and request features as [GitHub issues](https://github.com/talgent-work/workbench/issues/new/choose);
the forms ask for what you did, what you expected, what happened, and the release you run. Report
security vulnerabilities privately, as [SECURITY.md](SECURITY.md) describes. The project does not
accept outside pull requests.

## License

Apache License 2.0; see [LICENSE](LICENSE).
