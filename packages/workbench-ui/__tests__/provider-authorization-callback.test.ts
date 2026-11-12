import { beforeEach, expect, it, vi } from "vitest";
import { completeProviderAuthorization } from "../lib/marketplace-connections";

const completed: unknown[] = [];
vi.mock("../lib/account-service", () => ({ useCurrentUser: () => ({ data: { user: { id: "user" } } }) }));
vi.mock("../lib/notifications", () => ({ notify: vi.fn() }));
vi.mock("../lib/client", () => ({
  marketplaceClient: {
    completeProviderAuthorization: async (request: unknown) => {
      completed.push(request);
      return { authorization: { id: "authorization", providerLogin: "acme-support-bot" } };
    },
  },
}));

beforeEach(() => {
  completed.length = 0;
  window.sessionStorage.clear();
});

it("completes a connection for the application it began for, once, and returns to its page", async () => {
  window.sessionStorage.setItem("talgent.providerAuthorization.return", "/applications?app=desk&section=agent");
  window.sessionStorage.setItem("talgent.providerAuthorization.application", "desk");

  const { returnTo } = await completeProviderAuthorization("github", "code", "state");
  expect(completed).toEqual([expect.objectContaining({ provider: "github", code: "code", state: "state", applicationId: "desk" })]);
  expect(returnTo).toBe("/applications?app=desk&section=agent");
  expect(window.sessionStorage.length).toBe(0);
});

it("completes a person's own connection when no application began it", async () => {
  await completeProviderAuthorization("github", "code", "state");
  expect(completed).toEqual([expect.objectContaining({ applicationId: "" })]);
});
