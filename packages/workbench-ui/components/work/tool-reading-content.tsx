import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button } from "@heroui/react";
import { useWorkStreamSource } from "../../lib/work-stream-source";
import type { ToolPreview } from "./work-message-flow";
import { detailPageSize, MessageDetailContent } from "./tool-message-detail";

/** One call's original input and output, opened inline in the conversation. */
export function ToolReadingContent({ tool }: { tool: ToolPreview }) {
  const streamClient = useWorkStreamSource();
  const [offsets, setOffsets] = useState({ input: 0, output: 0 });
  const detail = useQuery({
    queryKey: ["tool-detail", tool.workId, tool.toolCallId, offsets.input, offsets.output],
    queryFn: ({ signal }) => streamClient.getToolDetail({ agentWorkId: tool.workId, toolCallId: tool.toolCallId, inputOffset: offsets.input, outputOffset: offsets.output, limit: detailPageSize }, { signal }),
    gcTime: 0, staleTime: Infinity, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
    refetchInterval: query => query.state.data && !query.state.data.output && !query.state.error ? 5000 : false,
    placeholderData: previous => previous,
  });
  const pages = detail.data;
  return <div className="wm-tool-reader wm-tool-detail">
    {detail.isPending && <p className="sr-only" role="status">Loading detail…</p>}
    {detail.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Could not load detail.</Alert.Description></Alert.Content><Button variant="ghost" size="sm" onPress={() => void detail.refetch()}>Retry</Button></Alert>}
    {pages && (["input", "output"] as const).map(side => {
      const page = pages[side];
      return <section key={side} aria-label={side === "input" ? "Tool input" : "Tool output"}>
        <h4>{side === "input" ? "Input" : "Output"}</h4>
        {page ? <MessageDetailContent emptyLabel={side === "input" ? "Empty input" : "Empty output"} detail={page} offset={offsets[side]} onPage={offset => setOffsets(current => ({ ...current, [side]: offset }))} /> : <p>{side === "input" ? "Input unavailable" : "No output yet"}</p>}
      </section>;
    })}
  </div>;
}
