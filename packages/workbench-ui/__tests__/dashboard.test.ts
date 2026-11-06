import { describe, expect, it } from "vitest";
import { formatDashboardTokens, parseDashboardWindow } from "../lib/dashboard";
import { compareMetric } from "../lib/dashboard-report";

describe("dashboard report periods", () => {
  it("defaults invalid URL windows to 24h", () => {
    expect(parseDashboardWindow(null)).toBe("24h");
    expect(parseDashboardWindow("invalid")).toBe("24h");
    expect(parseDashboardWindow("7d")).toBe("7d");
    expect(parseDashboardWindow("30d")).toBe("30d");
    expect(formatDashboardTokens(1250)).toBe("1.3k tok");
  });
  it("distinguishes unknown, new activity and zero change", () => {
    expect(compareMetric(1, null).direction).toBe("unknown");
    expect(compareMetric(1, 0).direction).toBe("new");
    expect(compareMetric(0, 0)).toEqual({ direction: "flat", percent: 0 });
    expect(compareMetric(15, 10)).toEqual({ direction: "up", percent: 50 });
    expect(compareMetric(5, 10)).toEqual({ direction: "down", percent: -50 });
  });
});
