import { createClient } from "@connectrpc/connect";
import { AgentWorkService } from "@talgent-work/workbench-contracts/agent/public/v1/work_pb";
import { PersonalWorkService } from "@talgent-work/workbench-contracts/agent/public/v1/personal_work_pb";
import { WorkFileService } from "@talgent-work/workbench-contracts/agent/public/v1/work_file_pb";
import { StreamGatewayService } from "@talgent-work/workbench-contracts/streamgateway/public/v1/streamgateway_pb";
import { TraceQueryService } from "@talgent-work/workbench-contracts/trace/public/v1/trace_pb";
import { MarketplaceService } from "@talgent-work/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { SessionService } from "@talgent-work/workbench-contracts/account/public/v1/session_pb";
import { ComputerService } from "@talgent-work/workbench-contracts/computer/public/v1/computer_pb";
import { transport, streamTransport, traceTransport } from "./transport";

// The Agent Workbench core's clients.
export const agentWorkClient = createClient(AgentWorkService, transport);
export const personalWorkClient = createClient(PersonalWorkService, transport);
export const workFileClient = createClient(WorkFileService, transport);
export const streamClient = createClient(StreamGatewayService, streamTransport);
export const traceClient = createClient(TraceQueryService, traceTransport);
export const marketplaceClient = createClient(MarketplaceService, transport);
export const sessionClient = createClient(SessionService, transport);
export const computerClient = createClient(ComputerService, transport);
