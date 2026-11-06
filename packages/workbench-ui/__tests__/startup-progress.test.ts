import { create } from "@bufbuild/protobuf";
import { StartupProgressSchema } from "@talgent-work/workbench-contracts/agent/public/v1/startup_pb";
import { describe, expect, it } from "vitest";
import { newestStartupProgress } from "../lib/startup-progress";

describe("Work startup facts", () => {
  it("never reopens completed startup from delayed snapshots or frames, including an earlier start", () => {
    const old = create(StartupProgressSchema, { requestId: "old", admitted: true, revision: "0000000003:0000000010", status: "preparing" });
    const done = create(StartupProgressSchema, { ...old, revision: "0000000003:0000000011", status: "completed" });
    expect(newestStartupProgress(done, old)).toBe(done);
    expect(newestStartupProgress(done, undefined)).toBe(done);
    const resumed = create(StartupProgressSchema, { requestId: "resume", admitted: true, revision: "0000000004:0000000001", status: "preparing" });
    expect(newestStartupProgress(done, resumed)).toBe(resumed);
    expect(newestStartupProgress(resumed, done)).toBe(resumed);
  });
});
