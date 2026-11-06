import { afterEach, describe, expect, it, vi } from "vitest";
import { refreshSession, safeInternalPath, signInHref, signInPagePath, signOut } from "../lib/session";

describe("session", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("asks Account to refresh once however many calls fail together", async () => {
    let resolve: (value: Response) => void = () => undefined;
    const fetchMock = vi.fn(() => new Promise<Response>(r => { resolve = r; }));
    vi.stubGlobal("fetch", fetchMock);
    const results = Promise.all([refreshSession(), refreshSession(), refreshSession()]);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    resolve(new Response(null, { status: 204 }));
    expect(await results).toEqual(["refreshed", "refreshed", "refreshed"]);
    expect(fetchMock).toHaveBeenCalledWith("/auth/refresh", expect.objectContaining({ method: "POST", headers: { "Content-Type": "application/json" } }));
  });

  it("tells a rejected session apart from an unreachable Account", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 503 })));
    expect(await refreshSession()).toBe("unavailable");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    expect(await refreshSession()).toBe("unavailable");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 401 })));
    expect(await refreshSession()).toBe("rejected");
  });

  // A device's Login Session ends with a rejected refresh; only signing in
  // again, a new page, starts another.
  it("asks Account no more once it rejected this device's session", async () => {
    vi.resetModules();
    const session = await import("../lib/session");
    const ended = vi.fn();
    session.subscribeSessionEnd(ended);
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 401 }));
    vi.stubGlobal("fetch", fetchMock);
    expect(await session.refreshSession()).toBe("rejected");
    expect(await session.refreshSession()).toBe("rejected");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(ended).toHaveBeenCalledTimes(1);
    expect(session.sessionEnded()).toBe(true);
  });

  it("fails sign-out when Account could not revoke the session", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 503 })));
    await expect(signOut()).rejects.toThrow("503");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    await expect(signOut()).resolves.toBeUndefined();
  });

  it("keeps return targets inside the Portal", () => {
    expect(safeInternalPath("/projects/a?tab=b")).toBe("/projects/a?tab=b");
    for (const unsafe of ["https://evil.example", "//evil.example", "/\\evil", "/auth/logout", "/login?next=/x", "/login#x", `/${"a".repeat(2048)}`, ""]) expect(safeInternalPath(unsafe)).toBe("/");
    // GitHub returns to one entry's host; the sign-in names the entry to come back to.
    expect(signInHref("/projects/a")).toBe(`/auth/github/login?next=${encodeURIComponent(`${window.location.origin}/projects/a`)}`);
    expect(signInHref("/auth/logout")).toBe(`/auth/github/login?next=${encodeURIComponent(`${window.location.origin}/`)}`);
    expect(signInPagePath("/")).toBe("/login");
  });

  // Signing in lands on the entry's home anyway, so naming it adds nothing.
  it("leaves out a return to the entry's home", () => {
    expect(signInPagePath("/home", "/home")).toBe("/login");
    expect(signInPagePath("/home")).toBe(`/login?next=${encodeURIComponent("/home")}`);
    expect(signInPagePath("/works/w-1", "/home")).toBe(`/login?next=${encodeURIComponent("/works/w-1")}`);
  });
});
