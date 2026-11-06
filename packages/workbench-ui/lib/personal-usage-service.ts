import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { useCurrentUser } from "./account-service";
import { traceClient } from "./client";
import type { DashboardReportPeriod } from "./dashboard-report";
import type { DashboardWindow } from "./dashboard";
import { useTokenAnalytics } from "./token-analytics";
import { traceMetricKinds } from "./trace-view-model";

/** The person's usage across all their Works, with or without a Project, as
 * Spend shows a Project's. */
export function usePersonalTokenAnalytics(window: DashboardWindow, period: DashboardReportPeriod) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  return useTokenAnalytics(["personal-token", userId], bounds => traceClient.getPersonalTraceMetrics({
    startTime: timestampFromDate(bounds.start),
    endTime: timestampFromDate(bounds.end),
    stepSeconds: bounds.stepSeconds,
    metricKinds: [...traceMetricKinds],
  }), window, period, Boolean(userId));
}
