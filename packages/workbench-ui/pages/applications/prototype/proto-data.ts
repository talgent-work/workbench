// PROTOTYPE (Applications): in-memory Applications for the Application pages
// prototype. Nothing persists; a reload restores the sample data.
import { createContext, use, useState } from "react";
import type { AgentIconValue } from "../../../lib/agent-icon";
import type { InstalledConnector, InstalledPlugin, WorkDefaults } from "./proto-catalog";

export type SecretState = "current" | "overlapping";
export interface ClientSecret { id: string; hint: string; issuedAt: number; state: SecretState; endsAt?: number }
export type WorkState = "running" | "waiting" | "idle" | "closed";
export interface ProtoWork { id: string; title: string; state: WorkState; labels: Record<string, string>; updatedAt: number; tokens: number }
export interface ProtoApplication {
  id: string; name: string; clientId: string; registeredAt: number;
  agent: { name: string; icon: AgentIconValue; soul: string };
  secrets: ClientSecret[];
  defaults: WorkDefaults;
  plugins: InstalledPlugin[];
  connectors: InstalledConnector[];
  /** Provider Authorizations: the connected account per Provider. */
  providers: Record<string, string>;
  works: ProtoWork[];
}

const hour = 3_600_000;
const day = 24 * hour;
const now = Date.now();
const randomHex = (length: number) => Array.from(crypto.getRandomValues(new Uint8Array(length)), byte => (byte % 16).toString(16)).join("");

const sampleWorks = (seed: Array<[string, WorkState, Record<string, string>, number, number]>): ProtoWork[] =>
  seed.map(([title, state, labels, ago, tokens], index) => ({ id: `w-${index}-${randomHex(4)}`, title, state, labels, updatedAt: now - ago, tokens }));

const initial: ProtoApplication[] = [
  {
    id: "app-support", name: "Support Desk", clientId: "app_7f3c9a1e52", registeredAt: now - 41 * day,
    agent: { name: "Desk Agent", icon: { glyph: "compass", color: "blue" }, soul: "You answer our customers’ support tickets. Be brief, cite the docs page you used and escalate billing questions." },
    secrets: [
      { id: "s-2", hint: "…k4Qz", issuedAt: now - 6 * hour, state: "current" },
      { id: "s-1", hint: "…9aLw", issuedAt: now - 30 * day, state: "overlapping", endsAt: now + 18 * hour },
    ],
    defaults: { modelId: "claude-sonnet", thinking: "medium", startupAgent: "ticket-triage:triage-lead" },
    plugins: [{ slug: "ticket-triage", enabled: true }, { slug: "docs-answers", enabled: true }, { slug: "release-notes", enabled: false }],
    connectors: [{ name: "linear", enabled: true }, { name: "slack", enabled: true }],
    providers: { github: "acme-support-bot", linear: "support@acme.example" },
    works: sampleWorks([
      ["Refund request for order 88412", "running", { tenant: "acme", user: "u-1932", channel: "email" }, 4 * 60_000, 18_400],
      ["Can’t sign in after password reset", "waiting", { tenant: "acme", user: "u-2210", channel: "chat" }, 22 * 60_000, 9_120],
      ["Export invoices to CSV", "idle", { tenant: "globex", user: "u-0071", channel: "email" }, 3 * hour, 41_800],
      ["Webhook retries keep failing", "idle", { tenant: "globex", user: "u-0444", channel: "chat" }, 9 * hour, 77_350],
      ["Change plan to annual billing", "closed", { tenant: "initech", user: "u-5120", channel: "email" }, 2 * day, 6_020],
      ["SAML setup for new workspace", "closed", { tenant: "acme", user: "u-1932", channel: "chat" }, 4 * day, 120_900],
    ]),
  },
  {
    id: "app-ci", name: "CI Triage", clientId: "app_c21e08bd4f", registeredAt: now - 9 * day,
    agent: { name: "Build Doctor", icon: { glyph: "flame", color: "amber" }, soul: "You read failed CI runs, find the first real error and propose the smallest fix as a pull request." },
    secrets: [{ id: "s-3", hint: "…Pw7e", issuedAt: now - 9 * day, state: "current" }],
    defaults: { modelId: "claude-opus", thinking: "high", startupAgent: "" },
    plugins: [{ slug: "repo-tools", enabled: true }, { slug: "test-writer", enabled: true }],
    connectors: [{ name: "sentry", enabled: true }],
    providers: {},
    works: sampleWorks([
      ["main: integration tests time out", "running", { repo: "payments", pipeline: "nightly" }, 2 * 60_000, 54_200],
      ["release/2.4: lint fails on generated code", "idle", { repo: "web", pipeline: "pr" }, 5 * hour, 12_480],
    ]),
  },
];

export const overlapHours = 24;

export interface Store {
  applications: ProtoApplication[];
  register: (name: string) => { application: ProtoApplication; secret: string };
  rotate: (id: string) => string;
  revoke: (id: string, secretId: string) => void;
  rename: (id: string, name: string) => void;
  saveAgent: (id: string, agent: ProtoApplication["agent"]) => void;
  remove: (id: string) => void;
  setPlugins: (id: string, change: (plugins: InstalledPlugin[]) => InstalledPlugin[]) => void;
  setConnectors: (id: string, change: (connectors: InstalledConnector[]) => InstalledConnector[]) => void;
  setProvider: (id: string, provider: string, account: string | null) => void;
  setDefaults: (id: string, defaults: Partial<WorkDefaults>) => void;
}

export const StoreContext = createContext<Store | null>(null);

const newSecret = () => `wbs_${randomHex(32)}`;
const hintOf = (secret: string) => `…${secret.slice(-4)}`;

export function useStoreState(): Store {
  const [applications, setApplications] = useState(initial);
  const update = (id: string, change: (application: ProtoApplication) => ProtoApplication) =>
    setApplications(list => list.map(application => application.id === id ? change(application) : application));
  const store: Store = {
    applications,
    register: name => {
      const secret = newSecret();
      const application: ProtoApplication = {
        id: `app-${randomHex(6)}`, name, clientId: `app_${randomHex(10)}`, registeredAt: Date.now(),
        agent: { name: `${name} Agent`, icon: { glyph: "bot", color: "green" }, soul: "" },
        secrets: [{ id: `s-${randomHex(4)}`, hint: hintOf(secret), issuedAt: Date.now(), state: "current" }],
        defaults: { modelId: "", thinking: "", startupAgent: "" }, plugins: [], connectors: [], providers: {}, works: [],
      };
      setApplications(list => [...list, application]);
      return { application, secret };
    },
    // Rotation ends an earlier overlap: the old current secret overlaps, any older one ends.
    rotate: id => {
      const secret = newSecret();
      update(id, application => ({
        ...application,
        secrets: [
          { id: `s-${randomHex(4)}`, hint: hintOf(secret), issuedAt: Date.now(), state: "current" },
          ...application.secrets.filter(item => item.state === "current").map(item => ({ ...item, state: "overlapping" as const, endsAt: Date.now() + overlapHours * hour })),
        ],
      }));
      return secret;
    },
    revoke: (id, secretId) => update(id, application => ({ ...application, secrets: application.secrets.filter(item => item.id !== secretId) })),
    rename: (id, name) => update(id, application => ({ ...application, name })),
    saveAgent: (id, agent) => update(id, application => ({ ...application, agent })),
    remove: id => setApplications(list => list.filter(application => application.id !== id)),
    setPlugins: (id, change) => update(id, application => ({ ...application, plugins: change(application.plugins) })),
    setConnectors: (id, change) => update(id, application => ({ ...application, connectors: change(application.connectors) })),
    setDefaults: (id, defaults) => update(id, application => ({ ...application, defaults: { ...application.defaults, ...defaults } })),
    setProvider: (id, provider, account) => update(id, application => {
      const providers = { ...application.providers };
      if (account) providers[provider] = account; else delete providers[provider];
      return { ...application, providers };
    }),
  };
  return store;
}

export function useProtoStore() {
  const store = use(StoreContext);
  if (!store) throw new Error("useProtoStore needs ProtoStoreProvider");
  return store;
}

export function formatAgo(ms: number) {
  const minutes = Math.round(ms / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}

export function formatIn(ms: number) {
  const hours = Math.max(0, Math.round(ms / hour));
  return hours < 1 ? "in under an hour" : `in ${hours}h`;
}

export const formatTokens = (tokens: number) => tokens >= 1000 ? `${(tokens / 1000).toFixed(1)}k` : String(tokens);

export type LabelFilter = Array<[string, string]>;
export const labelKey = ([key, value]: [string, string]) => `${key}=${value}`;
export const matches = (work: ProtoWork, filter: LabelFilter) => filter.every(([key, value]) => work.labels[key] === value);
