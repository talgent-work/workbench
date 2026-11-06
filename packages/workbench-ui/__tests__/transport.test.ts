import { Code, ConnectError, type UnaryRequest, type UnaryResponse } from "@connectrpc/connect";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sessionMocks = vi.hoisted(() => ({ refreshSession: vi.fn() }));
vi.mock("../lib/session", () => sessionMocks);

const { refreshOnUnauthenticated } = await import("../lib/transport");

const unauthenticated = () => new ConnectError("Jwt is expired", Code.Unauthenticated);
const request = (stream = false) => ({ stream, header: new Headers() }) as unknown as UnaryRequest;
const ok = { stream: false } as unknown as UnaryResponse;

describe("product API transport", () => {
  beforeEach(() => sessionMocks.refreshSession.mockReset());

  it("refreshes the session once and repeats the call after the token expired", async () => {
    sessionMocks.refreshSession.mockResolvedValue("refreshed");
    const next = vi.fn().mockRejectedValueOnce(unauthenticated()).mockResolvedValueOnce(ok);
    await expect(refreshOnUnauthenticated(next)(request())).resolves.toBe(ok);
    expect(sessionMocks.refreshSession).toHaveBeenCalledTimes(1);
    expect(next).toHaveBeenCalledTimes(2);
  });

  it("lets the error through when the session cannot be refreshed", async () => {
    sessionMocks.refreshSession.mockResolvedValue("rejected");
    const next = vi.fn().mockRejectedValue(unauthenticated());
    await expect(refreshOnUnauthenticated(next)(request())).rejects.toMatchObject({ code: Code.Unauthenticated });
    expect(next).toHaveBeenCalledTimes(1);
  });

  it("reports an unreachable Account as unavailable rather than signed out", async () => {
    sessionMocks.refreshSession.mockResolvedValue("unavailable");
    const next = vi.fn().mockRejectedValue(unauthenticated());
    await expect(refreshOnUnauthenticated(next)(request())).rejects.toMatchObject({ code: Code.Unavailable });
    expect(next).toHaveBeenCalledTimes(1);
  });

  it("does not refresh for other failures or retry a repeated rejection", async () => {
    const failing = vi.fn().mockRejectedValue(new ConnectError("down", Code.Unavailable));
    await expect(refreshOnUnauthenticated(failing)(request())).rejects.toMatchObject({ code: Code.Unavailable });
    expect(sessionMocks.refreshSession).not.toHaveBeenCalled();

    sessionMocks.refreshSession.mockResolvedValue("refreshed");
    const rejected = vi.fn().mockRejectedValue(unauthenticated());
    await expect(refreshOnUnauthenticated(rejected)(request())).rejects.toMatchObject({ code: Code.Unauthenticated });
    expect(rejected).toHaveBeenCalledTimes(2);
  });

  it("refreshes for a stream's reconnect without replaying it", async () => {
    sessionMocks.refreshSession.mockResolvedValue("refreshed");
    const next = vi.fn().mockRejectedValue(unauthenticated());
    await expect(refreshOnUnauthenticated(next)(request(true))).rejects.toMatchObject({ code: Code.Unauthenticated });
    expect(sessionMocks.refreshSession).toHaveBeenCalledTimes(1);
    expect(next).toHaveBeenCalledTimes(1);
  });

  it("never adds a bearer token from JavaScript", async () => {
    const seen = request();
    await refreshOnUnauthenticated(vi.fn().mockResolvedValue(ok))(seen);
    expect(seen.header.get("Authorization")).toBeNull();
  });
});
