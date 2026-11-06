import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { ManagedComputerSchema } from "@talgent/workbench-contracts/computer/public/v1/computer_pb";
import { describe, expect, it } from "vitest";
import { computerNameProblem, expiryLabel, managedComputer, normalizeUserCode, presenceLabel, registrationRefusal } from "../lib/computer-service";

const now = Date.parse("2026-11-02T12:00:00Z");

describe("Computer presence", () => {
  it("reads reachable as online, whatever else is set", () => {
    const computer = managedComputer(create(ManagedComputerSchema, { id: "c1", name: "laptop", presence: { reachable: true, unreachableSinceUnixMs: 5n } }));
    expect(presenceLabel(computer.presence, now)).toBe("Online");
  });

  it("says how long an unreachable Computer has been offline", () => {
    const computer = managedComputer(create(ManagedComputerSchema, { presence: { unreachableSinceUnixMs: BigInt(now - 3 * 3_600_000) } }));
    expect(presenceLabel(computer.presence, now)).toBe("Offline since 3h ago");
  });

  it("tells a Computer that never connected apart from one that went offline", () => {
    expect(presenceLabel(managedComputer(create(ManagedComputerSchema, { presence: { neverConnected: true } })).presence, now)).toBe("Never connected");
    expect(presenceLabel(managedComputer(create(ManagedComputerSchema, {})).presence, now)).toBe("Never connected");
    expect(presenceLabel({ kind: "offline", since: null }, now)).toBe("Offline");
  });
});

describe("Computer names", () => {
  it("accepts 1 to 64 characters after trimming", () => {
    expect(computerNameProblem("  ")).toBe("Enter a name.");
    expect(computerNameProblem(` ${"a".repeat(64)} `)).toBe("");
    expect(computerNameProblem("a".repeat(65))).toBe("Use at most 64 characters.");
  });
});

describe("registration codes", () => {
  it("normalizes what the person types into the code Talgent Computer shows", () => {
    expect(normalizeUserCode(" abcd efgh ")).toBe("ABCD-EFGH");
    expect(normalizeUserCode("ABCD-EFGH")).toBe("ABCD-EFGH");
    expect(normalizeUserCode(null)).toBe("");
  });

  it("maps refusals to what the person can do about them", () => {
    expect(registrationRefusal(new ConnectError("gone", Code.NotFound))).toBe("invalid");
    expect(registrationRefusal(new ConnectError("taken", Code.FailedPrecondition))).toBe("taken");
    expect(registrationRefusal(new ConnectError("down", Code.Unavailable))).toBe("failed");
  });

  it("says how long a registration has left", () => {
    expect(expiryLabel(new Date(now + 9.5 * 60_000), now)).toBe("Expires in 10m");
    expect(expiryLabel(new Date(now - 1), now)).toBe("Expired");
    expect(expiryLabel(null, now)).toBe("");
  });
});
