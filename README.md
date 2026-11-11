<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.png">
  <img alt="Talgent Workbench" src="docs/assets/logo-light.png" width="420">
</picture>

### Every person in the enterprise works with their own Agent.

Self-hosted. Claude Code native. Every Work in its own sandbox on your Kubernetes.

[![Release](https://img.shields.io/github/v/release/talgent-work/workbench?sort=semver&color=6a4cf5)](https://github.com/talgent-work/workbench/releases/latest)
[![License](https://img.shields.io/github/license/talgent-work/workbench?color=blue)](LICENSE)
[![npm @talgent/computer](https://img.shields.io/npm/v/@talgent/computer?label=%40talgent%2Fcomputer&logo=npm)](https://www.npmjs.com/package/@talgent/computer)

[Website](https://talgent.work) · [Get started](#get-started) · [Documentation](#documentation) ·
[Releases](https://github.com/talgent-work/workbench/releases) · [Report a bug](https://github.com/talgent-work/workbench/issues/new/choose)

<img alt="A Work in Talgent Workbench: the conversation with the Agent, its progress, Artifacts and token usage" src="docs/assets/work.webp" width="900">

</div>

AI has made individuals faster; most companies are still waiting. Talgent Workbench brings that
speed inside the company, under its own control. Every employee gets an AI Agent of their own,
built on Claude Code: start a Work, hand it files, follow what it does as it happens and get the
results back. Run several Works at once, and the same team takes on more.

Workbench runs in your own environment. Each Work gets an isolated sandbox, access is granted per
person, and every run's cost is in sight.

## What you can do

**Hand work to your Agent.** Describe the task, attach files, pick a repository, a model and the
Plugins it needs. Steer it while it runs, stop it, and pick it up later exactly where it left off.

**Watch it work.** The conversation streams live: what the Agent is thinking, the tools it calls,
its plan and its progress. Share a Work with colleagues so they can follow along, read-only, until
you revoke it.

**Keep everything it made.** Reports, pages, spreadsheets and decks the Agent publishes land in
your Library, next to what others have shared with you. Office documents, PDFs and images preview
in the browser.

**Extend it from the Marketplace.** Add Claude Code Plugins and Connectors your company offers, and
authorize your own GitHub account for the repositories a Work checks out.

**See what it costs.** Token use over time, by kind and by model, and the Works that used the most.

**Run on your own machine.** Connect a laptop or a workstation with Talgent Computer and run Works
there, with that machine's files and Claude login.

<table>
  <tr>
    <td width="50%"><img alt="New work: an instruction, a repository, the model and the Plugins the Work loads" src="docs/assets/new-work.webp"><p align="center"><sub>Start a Work with what it needs</sub></p></td>
    <td width="50%"><img alt="Library: Artifacts of a person's Works and of the Works shared with them" src="docs/assets/library.webp"><p align="center"><sub>Everything your Agents made, in your Library</sub></p></td>
  </tr>
  <tr>
    <td width="50%"><img alt="Usage: token use over time by kind and by model" src="docs/assets/usage.webp"><p align="center"><sub>Usage down to the token</sub></p></td>
    <td width="50%"><img alt="Marketplace: Claude Code Plugins by category" src="docs/assets/marketplace.webp"><p align="center"><sub>Plugins and Connectors from the Marketplace</sub></p></td>
  </tr>
</table>

<sub>Screenshots show sample data.</sub>

## Built for the enterprise

- **Your environment.** Deploy on your own Kubernetes cluster. One deployment serves one company,
  and its data stays there.
- **Isolated by default.** Every Work runs in its own sandbox, cut off from your private network.
  Credentials and model keys never enter it.
- **Any model.** Offer the models you choose, from any vendor endpoint Claude Code can use. People
  pick one when they start a Work.
- **Governed access.** People sign in with GitHub; each Work, file and share belongs to a person
  and reaches only who it is meant for.
- **Open source.** Apache-2.0. Read the code that runs your Agents.

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

- **Deploy Workbench** on your Kubernetes cluster with the [self-hosting guide](docs/self-hosting.md).
- **Connect your machine** with Talgent Computer: install `@talgent/computer` from npm and follow
  the Computers page of your Workbench (macOS, Linux and Windows).
- **Talk to us** about Talgent at [talgent.work](https://talgent.work).

## Documentation

| Topic | Where |
|---|---|
| Deploying on Kubernetes | [Self-hosting guide](docs/self-hosting.md) |
| Every setting of every process | [Configuration reference](docs/configuration.md) |
| Processes, stores and repository layout | [Architecture](docs/architecture.md) |
| Building, testing and releases | [Development](docs/development.md) |
| Talgent Computer and its gateway | [Talgent Computer protocol](docs/computer-protocol.md) |

## Contributing

Report bugs and request features as [GitHub issues](https://github.com/talgent-work/workbench/issues/new/choose);
the forms ask for what you did, what you expected, what happened, and the release you run. Report
security vulnerabilities privately, as [SECURITY.md](SECURITY.md) describes. The project does not
accept outside pull requests.

## License

Apache License 2.0; see [LICENSE](LICENSE).
