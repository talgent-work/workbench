import { describe, expect, it } from "vitest";
import { disconnectionNote, stopReasonNotice, workPresentation, workStatusName } from "../lib/work-state-presentation";

const since = new Date(2026, 10, 3, 14, 2);
const clock = since.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
const stops = new Date(2026, 10, 3, 14, 7).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
const before = new Date(2026, 10, 3, 14, 3).getTime();

describe("a Work's Disconnection beside its state", () => {
  it("keeps the state and adds the Disconnection while it lasts", () => {
    expect(workPresentation({ state: "working", disconnectedSince: since })).toMatchObject({ label: "Working · Disconnected", color: "warning" });
    expect(workPresentation({ state: "working", waiting: true, disconnectedSince: since })?.label).toBe("Waiting for your answer · Disconnected");
  });

  it("tells when the Work stops or closes, counted from the loss", () => {
    expect(disconnectionNote("working", since, before)).toBe(`Disconnected since ${clock}. The Work stops at ${stops} unless it reconnects by then.`);
    expect(disconnectionNote("stopping", since, before)).toBe(`Disconnected since ${clock}. The Work stops at ${stops} unless it reconnects by then.`);
    expect(disconnectionNote("closing", since, before)).toBe(`Disconnected since ${clock}. The Work closes at ${stops} unless it reconnects by then.`);
    expect(disconnectionNote("working", since, since.getTime() + 6 * 60_000)).toBe(`Disconnected since ${clock}. The Work stops now.`);
  });

  it("names the status with what it means, not only its label", () => {
    expect(workStatusName("Work: ", { label: "Working · Disconnected", description: "Disconnected since 14:02." })).toBe("Work: Working · Disconnected. Disconnected since 14:02.");
    expect(workStatusName("Work: ", { label: "Working" })).toBe("Work: Working");
  });

  it("promises no stop for a starting Work", () => {
    expect(workPresentation({ state: "starting", disconnectedSince: since })?.description).toBe(`Disconnected since ${clock}.`);
  });

  it("shows nothing of a Disconnection while observed or once the Work has stopped", () => {
    expect(workPresentation({ state: "working", disconnectedSince: null })).toMatchObject({ label: "Working", color: "success" });
    expect(workPresentation({ state: "stopped", disconnectedSince: since })?.label).toBe("Stopped");
  });

  it("names a disconnection as the stop reason of a Stopped Work only", () => {
    expect(workPresentation({ state: "stopped", stateReason: "disconnected" })?.label).toBe("Stopped · Disconnected");
    expect(workPresentation({ state: "stopped", stateReason: "user_stopped" })?.label).toBe("Stopped");
    expect(workPresentation({ state: "working", stateReason: "disconnected" })?.label).toBe("Working");
    expect(stopReasonNotice("stopped", "disconnected")).toBe("Stopped after being disconnected for 5 minutes.");
    expect(stopReasonNotice("stopped", "user_stopped")).toBeUndefined();
    expect(stopReasonNotice("working", "disconnected")).toBeUndefined();
  });
});
