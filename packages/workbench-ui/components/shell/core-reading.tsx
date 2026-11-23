import { workFileKey } from "../../lib/work-file-service";
import { ToolPreviewReading } from "../work/tool-preview-reading";
import { OwnWorkUsage, WorkFileReading, WorkWorkspaceReading } from "./core-reading-views";
import type { ReadingRenderer } from "./reading-context";

/** The core's readings: a tool call's preview, a Work's Token Usage, a
 * Work's file and its workspace. */
export const coreReading: ReadingRenderer = content => {
  switch (content.kind) {
    case "tool": return { key: `${content.tool.workId}:${content.tool.toolCallId}`, label: content.label, title: content.tool.title,
      render: ({ toolbar }) => <ToolPreviewReading key={`${content.tool.workId}:${content.tool.toolCallId}`} tool={content.tool} toolbar={toolbar} /> };
    case "trace": return { key: `trace:${content.workId}`, label: content.label, title: "Token Usage", render: slots => <OwnWorkUsage content={content} slots={slots} /> };
    case "work-file": return { key: `${content.workId}:${workFileKey(content.file)}`, label: content.label, title: content.file.name,
      render: ({ toolbar }) => <WorkFileReading key={`${workFileKey(content.file)}:${String(content.file.updatedAt?.seconds ?? "")}`} workId={content.workId} file={content.file} toolbar={toolbar} /> };
    case "work-workspace": return { key: `workspace:${content.workId}`, label: content.label, title: "Workspace", ownLocation: true,
      render: ({ toolbar, location }) => <WorkWorkspaceReading key={content.workId} content={content} location={location} toolbar={toolbar} /> };
    default: return null;
  }
};
