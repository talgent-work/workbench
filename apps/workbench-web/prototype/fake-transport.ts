// PROTOTYPE ONLY (personal variables UI): an in-memory core so the Profile
// page renders without a backend. Never imported by the real app.
import type { DescService } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport, type ConnectRouter } from "@connectrpc/connect";
import { AgentWorkService } from "@talgent/contracts/agent/public/v1/work_pb";
import { WorkService } from "@talgent/contracts/agent/public/v2/work_pb";
import { SessionService } from "@talgent/contracts/account/public/v1/session_pb";
import { MarketplaceService } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { ComputerService } from "@talgent/contracts/computer/public/v1/computer_pb";

type Fakes = Record<string, (request: never) => unknown>;

function route(router: ConnectRouter, service: DescService, fakes: Fakes) {
  const implementation: Record<string, (request: never) => unknown> = {};
  for (const method of service.methods) {
    implementation[method.localName] = request => {
      const fake = fakes[method.localName];
      if (!fake) throw new ConnectError(`${method.localName} is not faked in the prototype`, Code.Unimplemented);
      return fake(request);
    };
  }
  router.service(service, implementation as never);
}

let soul = "Answer briefly. Ask before large refactors.";
let instructions = "# Working here\n- Run the tests before you push";

export const transport = createRouterTransport(router => {
  route(router, SessionService, {
    getCurrentSession: () => ({ session: { userId: "e6c7e756-007d-5236-a492-7f4b9be0b2e8", displayName: "Octo Cat", username: "octocat", primaryEmail: "octo@example.test" } }),
  });
  route(router, AgentWorkService, {
    getMyWorkAgent: () => ({ agentId: "prototype-agent", appearance: { name: "Octo Cat's Agent" } }),
    getAgentSoul: () => ({ soul }),
    changeAgentSoul: (request: { soul: string }) => { soul = request.soul; return { soul }; },
    getAgentWorkspaceInstructions: () => ({ instructions }),
    changeAgentWorkspaceInstructions: (request: { instructions: string }) => { instructions = request.instructions; return { instructions }; },
  });
  route(router, WorkService, {
    listWorks: () => ({ works: [] }),
    getWorkStartOptions: () => ({ options: { models: [], plugins: [], connectors: [] } }),
    listWorkStartRepositories: () => ({ repositories: { repositories: [], recentRepositoryIds: [], repositoriesUnavailable: false } }),
  });
  route(router, ComputerService, { listUsableComputers: () => ({ computers: [{ id: "cloud", name: "Cloud", kind: 1 }] }) });
  route(router, MarketplaceService, {
    listProviders: () => ({ providers: [] }),
    listConnectors: () => ({ connectors: [] }),
    listInstalledConnectors: () => ({ connectors: [] }),
    listPlugins: () => ({ plugins: [] }),
    listInstalledPlugins: () => ({ plugins: [] }),
  });
});
export const streamTransport = transport;
export const traceTransport = transport;
