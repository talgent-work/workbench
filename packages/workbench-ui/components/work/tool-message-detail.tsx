import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button } from "@heroui/react";
import { MessageDetailFormat } from "@talgent-work/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import type { GetMessageDetailResponse } from "@talgent-work/workbench-contracts/streamgateway/public/v1/streamgateway_pb";
import { useWorkStreamSource } from "../../lib/work-stream-source";

export const detailPageSize = 16384;

// Mount only while the detail is open. Each reader retains one bounded page;
// closing it aborts the request and releases the query's original body.
export function ToolMessageDetail({ workId, messageId }: { workId: string; messageId: string }) {
  const streamClient = useWorkStreamSource();
  const [position, setPosition] = useState({ workId, messageId, offset: 0 });
  const offset = position.workId === workId && position.messageId === messageId ? position.offset : 0;
  const detail = useQuery({
    queryKey: ["message-detail", workId, messageId, offset],
    queryFn: ({ signal }) => streamClient.getMessageDetail({ agentWorkId: workId, messageId, offset, limit: detailPageSize }, { signal }),
    gcTime: 0, staleTime: Infinity, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false,
  });
  return <div className="wm-tool-detail">
    {detail.isPending && <p className="sr-only" role="status">Loading detail…</p>}
    {detail.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Could not load detail.</Alert.Description></Alert.Content><Button variant="ghost" size="sm" onPress={() => void detail.refetch()}>Retry</Button></Alert>}
    {detail.data && <MessageDetailContent detail={detail.data} offset={offset} onPage={offset => setPosition({ workId, messageId, offset })} />}
  </div>;
}

export function MessageDetailContent({ detail, offset, onPage, emptyLabel = "Empty output" }: { emptyLabel?: string; detail: GetMessageDetailResponse; offset: number; onPage: (offset: number) => void }) {
  return <>
      {detail.format === MessageDetailFormat.UNSPECIFIED ? <p>No detail available</p>
        : detail.totalLength === 0 ? <p>{emptyLabel}</p>
        : <pre tabIndex={0} aria-label="Original tool detail">{detail.text}</pre>}
      {detail.totalLength > detailPageSize && <nav aria-label="Detail pages" className="wm-detail-pages">
        <Button variant="ghost" size="sm" isDisabled={offset === 0} onPress={() => onPage(Math.max(0, offset - detailPageSize))}>Previous page</Button>
        <span>{Math.floor(offset / detailPageSize) + 1} / {Math.ceil(detail.totalLength / detailPageSize)}</span>
        <Button variant="ghost" size="sm" isDisabled={detail.nextOffset >= detail.totalLength} onPress={() => onPage(detail.nextOffset)}>Next page</Button>
      </nav>}
  </>;
}
