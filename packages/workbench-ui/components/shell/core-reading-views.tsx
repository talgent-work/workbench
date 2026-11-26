import { useCallback, useMemo } from "react";
import { WorkFileKind, type WorkFile } from "@talgent/contracts/agent/public/v1/work_file_pb";
import { useCurrentUser } from "../../lib/account-service";
import { useAgentWork } from "../../lib/agent-work-service";
import { requestWorkFilePreview, useDownloadWorkFile, useWorkFiles, workFileKey, workFilePreview } from "../../lib/work-file-service";
import { workWorkspaceSource } from "../../lib/work-workspace-service";
import { WorkspaceReader } from "../workspace/workspace-reader";
import { useOwnWorkUsageStatus } from "../analytics/work-usage/work-status";
import { WorkUsageReader } from "../analytics/work-usage/work-usage-reader";
import { FileReading } from "./file-reading";
import type { ReadingContent, ReadingSlots } from "./reading-context";

/** The Work's Token Usage as its owner reads it. */
export function OwnWorkUsage({ content, slots }: { content: Extract<ReadingContent, { kind: "trace" }>; slots: ReadingSlots }) {
  const status = useOwnWorkUsageStatus(content.workId);
  return <WorkUsageReader key={content.workId} workCaller={content.workCaller} agentWorkId={content.workId} createdAt={content.createdAt} status={status} toolbar={slots.toolbar} />;
}

/** A Work's file, previewed through Agent. */
export function WorkFileReading({ workId, file, toolbar }: { workId: string; file: WorkFile; toolbar: HTMLElement | null }) {
  const userId = useCurrentUser().data?.user?.id;
  const download = useDownloadWorkFile(workId);
  if (!userId) return null;
  return <FileReading name={file.name} toolbar={toolbar}
    // A replaced file keeps its position; its change time tells new content.
    verdictKey={[userId, workId, workFileKey(file), String(file.updatedAt?.seconds ?? "")]}
    loadVerdict={() => workFilePreview(workId, file)}
    requestRender={() => requestWorkFilePreview(workId, file)}
    download={{ run: () => download.mutate(file), isPending: download.isPending, error: download.error, reset: download.reset }} />;
}

/** A Standalone Work's workspace, read through Agent. Its owner's listings
 * refresh on their own while the Work runs, as the Work's snapshot tells; a
 * Work Share holder, who reads no snapshot, refreshes them by hand. */
export function WorkWorkspaceReading({ content, location, toolbar }: { content: Extract<ReadingContent, { kind: "work-workspace" }>; location: HTMLElement | null; toolbar: HTMLElement | null }) {
  const running = useAgentWork(content.workId).data?.work?.state === "working";
  const source = useMemo(() => workWorkspaceSource(content.workId), [content.workId]);
  const Attachment = useCallback(({ name, toolbar }: { name: string; toolbar: HTMLElement | null }) => <WorkAttachmentReading workId={content.workId} name={name} toolbar={toolbar} />, [content.workId]);
  return <WorkspaceReader source={source} running={running} artifactsNote="Its Artifacts stay with the Work."
    Attachment={Attachment} location={location} tools={toolbar} />;
}

// An inputs/ entry reads as the Work's Attachment of that name.
function WorkAttachmentReading({ workId, name, toolbar }: { workId: string; name: string; toolbar: HTMLElement | null }) {
  const files = useWorkFiles(workId);
  const file = files.data?.find(item => item.kind === WorkFileKind.ATTACHMENT && item.name === name);
  if (!file) return files.isPending ? <p className="sr-only" role="status">Loading…</p> : <p className="wsb-muted" role="status">This Attachment is no longer in the Work.</p>;
  return <WorkFileReading key={`${name}:${String(file.updatedAt?.seconds ?? "")}`} workId={workId} file={file} toolbar={toolbar} />;
}
