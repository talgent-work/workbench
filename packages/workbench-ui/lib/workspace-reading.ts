import { useEffect, useState } from "react";
import { useIsFetching, useQuery, useQueryClient } from "@tanstack/react-query";
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import { WorkWorkspaceEntryKind as EntryKind, WorkWorkspaceState as State } from "@talgent/contracts/agent/public/v1/work_workspace_pb";
import type { PreviewVerdict } from "../components/preview/preview-verdict";
import { previewVerdict, type RelayedPreview } from "./preview-verdict";

/** How often expanded folders refresh while the Work runs and the page is
 * visible. */
export const WORKSPACE_REFRESH_MS = 10_000;

// A Work's workspace as a context relays it. Agent's workspace contract
// numbers its states and entry kinds, and a context relaying it keeps that
// numbering, so one reading of Agent's enums serves every relay.
type RelayedEntry = { name: string; kind: number; sizeBytes: bigint; modifiedAt?: Timestamp; attachmentName: string };
type RelayedListing = { state: number; entries: RelayedEntry[]; total: number };

/** Where one Work's workspace is read: Agent for its own readers, or a
 * context that admits its readers itself. `scope` keeps one workspace's
 * reads apart from another's. */
export type WorkspaceSource = {
  scope: string;
  browse: (path: string, signal: AbortSignal) => Promise<RelayedListing>;
  preview: (path: string, signal: AbortSignal) => Promise<{ state: number; preview?: RelayedPreview }>;
  download: (path: string) => Promise<{ state: number; accessUrl: string }>;
};

export type WorkspaceEntry = {
  /** Path relative to /workspace. */
  path: string;
  name: string;
  kind: "directory" | "file" | "link";
  size: number;
  modifiedAt: Date | null;
  /** Under inputs/: the Work's Attachment this entry is. */
  attachmentName?: string;
};

export type WorkspaceListing = { entries: WorkspaceEntry[]; total: number };

/** Why a Work's workspace cannot be read. */
export type WorkspaceUnavailableState = "not_created" | "closed" | "reclaimed" | "offline";

const unavailableMessages: Record<WorkspaceUnavailableState, string> = {
  not_created: "This Work hasn't created its workspace yet.",
  closed: "The Work is closed, so its workspace is unavailable.",
  reclaimed: "This workspace was reclaimed after the Work closed.",
  // A Managed Work's files stay on its Computer, read only while it's reachable.
  offline: "This Computer is offline. Files are available again when it reconnects.",
};

export class WorkspaceUnavailable extends Error {
  constructor(readonly state: WorkspaceUnavailableState) {
    super(unavailableMessages[state]);
  }
}

const unavailableStates: Partial<Record<number, WorkspaceUnavailableState>> = {
  [State.NOT_CREATED]: "not_created", [State.RECLAIMED]: "reclaimed", [State.UNAVAILABLE]: "offline",
};

function available(state: number) {
  if (state === State.AVAILABLE) return;
  // A state this build does not know reads as closed: nothing is offered.
  throw new WorkspaceUnavailable(unavailableStates[state] ?? "closed");
}

const entryKinds: Partial<Record<number, WorkspaceEntry["kind"]>> = {
  [EntryKind.DIRECTORY]: "directory", [EntryKind.FILE]: "file", [EntryKind.LINK]: "link",
};

const workspaceQueryKey = (scope: string) => ["workspace", scope] as const;

async function listDirectory(source: WorkspaceSource, path: string, signal: AbortSignal): Promise<WorkspaceListing> {
  const response = await source.browse(path, signal);
  available(response.state);
  return {
    total: response.total,
    entries: response.entries.map(entry => ({
      path: path ? `${path}/${entry.name}` : entry.name, name: entry.name, kind: entryKinds[entry.kind] ?? "file",
      size: Number(entry.sizeBytes), modifiedAt: entry.modifiedAt ? timestampDate(entry.modifiedAt) : null,
      attachmentName: entry.attachmentName || undefined,
    })),
  };
}

/** Whether the workspace may still change: the Work runs and the page shows. */
export function useAutoRefresh(running: boolean) {
  const [visible, setVisible] = useState(() => document.visibilityState !== "hidden");
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  return running && visible;
}

export function useWorkspaceDirectory(source: WorkspaceSource, path: string, auto: boolean, enabled = true) {
  return useQuery({
    queryKey: [...workspaceQueryKey(source.scope), "dir", path],
    queryFn: ({ signal }) => listDirectory(source, path, signal),
    enabled, retry: false, refetchOnWindowFocus: false,
    refetchInterval: auto ? WORKSPACE_REFRESH_MS : false, refetchIntervalInBackground: false,
  });
}

/** Refreshes every listed folder and the open file, and tells when it last did. */
export function useWorkspaceRefresh(scope: string) {
  const client = useQueryClient();
  const [refreshedAt, setRefreshedAt] = useState(() => new Date());
  useEffect(() => client.getQueryCache().subscribe(event => {
    const [kind, key] = event.query.queryKey;
    if (event.type === "updated" && event.action.type === "success" && kind === "workspace" && key === scope) setRefreshedAt(new Date());
  }), [client, scope]);
  const fetching = useIsFetching({ queryKey: workspaceQueryKey(scope) }) > 0;
  return { refreshedAt, fetching, refresh: () => client.invalidateQueries({ queryKey: workspaceQueryKey(scope) }) };
}

export async function workspaceFileVerdict(source: WorkspaceSource, path: string, signal: AbortSignal): Promise<PreviewVerdict> {
  const response = await source.preview(path, signal);
  available(response.state);
  return previewVerdict(response.preview);
}

export async function downloadWorkspaceFile(source: WorkspaceSource, path: string, open: (url: string, target?: string, features?: string) => unknown = window.open.bind(window)) {
  const response = await source.download(path);
  available(response.state);
  if (!response.accessUrl) throw new Error("The download link is missing.");
  open(response.accessUrl, "_blank", "noopener,noreferrer");
}
