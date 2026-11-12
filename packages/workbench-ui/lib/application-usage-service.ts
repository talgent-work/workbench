import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { traceClient } from "./client";
import { emptyTokenUsage, tokenMetricKinds, tokenUsageFromMeasurements, type TokenUsage } from "./token-usage";

// An Application's usage as Observability reads it for its Application
// Owner (TraceQueryService.GetApplicationTraceMetrics): its Works' tokens,
// broken down by one Work Label key that Agent resolves; Observability keeps
// no label.

/** Usage of the Works carrying one value of the breakdown key, or, with
 * labelled false, of those without it. */
export type LabelUsage = { value: string; labelled: boolean; usage: TokenUsage; workCount: number };

export type ApplicationUsage = {
  usage: TokenUsage;
  /** The Work Label keys its measured Works carry. */
  labelKeys: string[];
  /** The key breakdown is by; empty when the Works carry none. */
  breakdownKey: string;
  breakdown: LabelUsage[];
};

/** The window the Overview reads: the measurements kept, 30 days. */
export const usageWindowDays = 30;
const dayMs = 86_400_000;

/** The Application's token usage over the last 30 days, broken down by the
 * key the owner picked, or, while their Works carry no such key, by the first
 * key they carry. */
export function useApplicationUsage(applicationId: string, pickedKey: string) {
  return useQuery({
    queryKey: ["application-usage", applicationId, pickedKey],
    placeholderData: keepPreviousData,
    queryFn: async (): Promise<ApplicationUsage> => {
      // Whole days, so a refetch within the day reads the same window.
      const end = new Date(Math.ceil(Date.now() / dayMs) * dayMs);
      const read = (breakdownKey: string) => traceClient.getApplicationTraceMetrics({
        applicationId,
        startTime: timestampFromDate(new Date(end.getTime() - usageWindowDays * dayMs)),
        endTime: timestampFromDate(end),
        stepSeconds: usageWindowDays * 86_400,
        metricKinds: [...tokenMetricKinds],
        breakdownKey,
        listLabelKeys: true,
      });
      let breakdownKey = pickedKey;
      let response = await read(breakdownKey);
      if (!response.labelKeys.includes(breakdownKey)) {
        breakdownKey = response.labelKeys[0] ?? "";
        if (breakdownKey) response = await read(breakdownKey);
      }
      return {
        usage: response.totals.length ? tokenUsageFromMeasurements(response.totals) : emptyTokenUsage(),
        labelKeys: response.labelKeys,
        breakdownKey,
        breakdown: response.breakdown.map(group => ({ value: group.value, labelled: group.labelled, usage: tokenUsageFromMeasurements(group.totals), workCount: group.workCount })),
      };
    },
  });
}
