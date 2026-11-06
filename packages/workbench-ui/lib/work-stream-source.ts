import { createContext, useContext } from "react";
import type { MessageInitShape } from "@bufbuild/protobuf";
import type { CallOptions } from "@connectrpc/connect";
import {
  GetMessageDetailRequestSchema, GetToolDetailRequestSchema, ListMessagesBySessionRequestSchema, WatchStreamRequestSchema,
  type GetMessageDetailResponse, type GetToolDetailResponse, type ListMessagesBySessionResponse, type WatchStreamResponse,
} from "@talgent/workbench-contracts/streamgateway/public/v1/streamgateway_pb";
import { streamClient } from "./client";

/** Where a Work's message stream is read from: Streamgateway for its owner
 * and Work Share holders by default; an entry may supply another source. */
export interface WorkStreamSource {
  watchStream(request: MessageInitShape<typeof WatchStreamRequestSchema>, options?: CallOptions): AsyncIterable<WatchStreamResponse>;
  listMessagesBySession(request: MessageInitShape<typeof ListMessagesBySessionRequestSchema>, options?: CallOptions): Promise<ListMessagesBySessionResponse>;
  getMessageDetail(request: MessageInitShape<typeof GetMessageDetailRequestSchema>, options?: CallOptions): Promise<GetMessageDetailResponse>;
  getToolDetail(request: MessageInitShape<typeof GetToolDetailRequestSchema>, options?: CallOptions): Promise<GetToolDetailResponse>;
}

// Delegates per call so the client is resolved where a stream is read, not
// when this module loads.
export const streamGatewaySource: WorkStreamSource = {
  watchStream: (request, options) => streamClient.watchStream(request, options),
  listMessagesBySession: (request, options) => streamClient.listMessagesBySession(request, options),
  getMessageDetail: (request, options) => streamClient.getMessageDetail(request, options),
  getToolDetail: (request, options) => streamClient.getToolDetail(request, options),
};

export const WorkStreamSourceContext = createContext<WorkStreamSource>(streamGatewaySource);

export function useWorkStreamSource() {
  return useContext(WorkStreamSourceContext);
}
