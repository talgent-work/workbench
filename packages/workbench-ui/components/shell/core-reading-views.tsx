import type { WorkFile } from "@talgent/contracts/agent/public/v1/work_file_pb";
import { useCurrentUser } from "../../lib/account-service";
import { requestWorkFilePreview, useDownloadWorkFile, workFileKey, workFilePreview } from "../../lib/work-file-service";
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
