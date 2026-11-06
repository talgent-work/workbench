import { describe, expect, it } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { adaptWorkProgress } from "../lib/work-progress";

describe("adaptWorkProgress", () => {
  // Intent (real): persisted Work progress is the Overview source of truth.
  it("maps Work progress snapshots into Overview progress items", () => {
    const updatedAt = new Date("2026-06-09T08:00:00Z");

    expect(adaptWorkProgress({
      updatedAt: timestampFromDate(updatedAt),
      items: [
        { content: "Create outline", status: "completed", activeForm: "Creating outline" },
        { content: "Write report", status: "in_progress" },
      ],
    })).toEqual({
      updatedAt,
      items: [
        { content: "Create outline", status: "completed", activeForm: "Creating outline" },
        { content: "Write report", status: "in_progress" },
      ],
    });
  });

  // Intent (real): live WorkState can clear Progress after the last task is deleted.
  it("maps empty Work progress snapshots to null", () => {
    expect(adaptWorkProgress({ items: [] })).toBeNull();
  });
});
