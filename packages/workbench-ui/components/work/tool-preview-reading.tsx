import { Alert, Button } from "@heroui/react";
import { useQuery } from "@tanstack/react-query";
import { useWorkStreamSource } from "../../lib/work-stream-source";
import { ToolOutputPreview } from "./tool-output-preview";
import type { ToolPreview } from "./work-message-flow";

/**
 * A Read, Write or Edit call in the reading area, shown like an opened file:
 * what it read, wrote or changed, never its original input or output.
 */
export function ToolPreviewReading({ tool, toolbar }: { tool: ToolPreview; toolbar: HTMLElement | null }) {
  const streamClient = useWorkStreamSource();
  const detail = useQuery({
    queryKey: ["tool-preview", tool.workId, tool.toolCallId],
    // The preview comes with the first page; one code point of each side is
    // enough to tell a recorded result from one still to come.
    queryFn: ({ signal }) => streamClient.getToolDetail({ agentWorkId: tool.workId, toolCallId: tool.toolCallId, inputOffset: 0, outputOffset: 0, limit: 1 }, { signal }),
    gcTime: 0, staleTime: Infinity, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
    refetchInterval: query => query.state.data && !query.state.data.output && !query.state.error ? 5000 : false,
  });
  return <div className="pr-file-document">
    {detail.isPending && <p className="sr-only" role="status">Loading preview…</p>}
    {detail.isError && <Alert status="danger" className="pv-alert" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Could not load the preview.</Alert.Description></Alert.Content><Button size="sm" variant="secondary" onPress={() => void detail.refetch()}>Retry preview</Button></Alert>}
    {detail.data && (detail.data.preview ? <ToolOutputPreview preview={detail.data.preview} toolbar={toolbar} />
      : <Alert status="default" className="pv-alert" role="status"><Alert.Indicator /><Alert.Content><Alert.Description>{detail.data.output ? "This call has nothing to preview." : "Waiting for this call to finish…"}</Alert.Description></Alert.Content></Alert>)}
  </div>;
}
