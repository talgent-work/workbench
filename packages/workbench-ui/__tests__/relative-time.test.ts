import { expect, it } from "vitest";
import { overlapText } from "../lib/application-service";
import { timeUntil } from "../lib/relative-time";

const now = Date.UTC(2026, 10, 12, 8, 0, 0);
const after = (ms: number) => new Date(now + ms);

it("says how long until a moment, in hours for up to two days", () => {
  expect(timeUntil(after(-1000), now)).toBe("now");
  expect(timeUntil(after(30_000), now)).toBe("in 1m");
  expect(timeUntil(after(59 * 60_000), now)).toBe("in 59m");
  expect(timeUntil(after(3_600_000), now)).toBe("in 1h");
  expect(timeUntil(after(24 * 3_600_000), now)).toBe("in 24h");
  expect(timeUntil(after(47 * 3_600_000 + 59 * 60_000), now)).toBe("in 47h");
  expect(timeUntil(after(72 * 3_600_000), now)).toBe("in 3d");
});

it("words a rotation overlap in hours, or minutes when it is not whole hours", () => {
  expect(overlapText(24 * 3_600_000)).toBe("24 hours");
  expect(overlapText(3_600_000)).toBe("1 hour");
  expect(overlapText(90 * 60_000)).toBe("90 minutes");
});
