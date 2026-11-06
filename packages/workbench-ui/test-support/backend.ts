import type { DescService } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type ConnectRouter, type HandlerContext } from "@connectrpc/connect";
import { AgentWorkService } from "@talgent/workbench-contracts/agent/public/v1/work_pb";
import { PersonalWorkService } from "@talgent/workbench-contracts/agent/public/v1/personal_work_pb";
import { WorkFileService } from "@talgent/workbench-contracts/agent/public/v1/work_file_pb";
import { SessionService } from "@talgent/workbench-contracts/account/public/v1/session_pb";
import { ComputerService } from "@talgent/workbench-contracts/computer/public/v1/computer_pb";
import { MarketplaceService } from "@talgent/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { StreamGatewayService } from "@talgent/workbench-contracts/streamgateway/public/v1/streamgateway_pb";
import { TraceQueryService } from "@talgent/workbench-contracts/trace/public/v1/trace_pb";
import { vi } from "vitest";

// The single frontend test boundary: pages render through the real
// routes while every ConnectRPC client talks to an in-memory router. Tests
// replace service methods per case and assert on the requests received. An
// app built on workbench-ui routes its own services beside these.
type Handler = (request: never, context: HandlerContext) => unknown;
export type Fakes = Record<string, Handler | undefined>;

/** The core's services, each a set of faked methods. */
export const services = { session: {} as Fakes, marketplace: {} as Fakes, agent: {} as Fakes, stream: {} as Fakes, trace: {} as Fakes, personal: {} as Fakes, workFile: {} as Fakes, computer: {} as Fakes };

/** Routes service to fakes(); a method nobody faked is unimplemented. */
export function routeFakes(router: ConnectRouter, service: DescService, fakes: () => Fakes) {
  const implementation: Record<string, Handler> = {};
  for (const method of service.methods) {
    implementation[method.localName] = (request, context) => {
      const fake = fakes()[method.localName];
      if (!fake) throw new ConnectError(`${method.localName} is not faked`, Code.Unimplemented);
      return fake(request, context);
    };
  }
  router.service(service, implementation as never);
}

export function routeCoreServices(router: ConnectRouter) {
  routeFakes(router, SessionService, () => services.session);
  routeFakes(router, MarketplaceService, () => services.marketplace);
  routeFakes(router, AgentWorkService, () => services.agent);
  routeFakes(router, StreamGatewayService, () => services.stream);
  routeFakes(router, TraceQueryService, () => services.trace);
  routeFakes(router, PersonalWorkService, () => services.personal);
  routeFakes(router, WorkFileService, () => services.workFile);
  routeFakes(router, ComputerService, () => services.computer);
}

export const testTransport = createRouterTransport(routeCoreServices);

/** Stands in for lib/transport: vi.mock("@talgent/workbench-ui/lib/transport", ...). */
export const transportModule = { transport: testTransport, streamTransport: testTransport, traceTransport: testTransport };

export const userId = "e6c7e756-007d-5236-a492-7f4b9be0b2e8";

/** A signed-in person and nothing else; each test adds the services it exercises. */
export function resetServices() {
  for (const fakes of Object.values(services)) for (const key of Object.keys(fakes)) delete fakes[key];
  Object.assign(services.session, {
    getCurrentSession: () => ({ session: { userId, displayName: "Octo Cat", username: "octocat", primaryEmail: "octo@example.test" } }),
  });
}

// Records requests so tests assert on what reached the backend.
export function recorder<Request, Response>(respond: (request: Request) => Response) {
  const calls: Request[] = [];
  const handler = vi.fn((request: Request) => { calls.push(request); return respond(request); });
  return Object.assign(handler, { calls });
}
