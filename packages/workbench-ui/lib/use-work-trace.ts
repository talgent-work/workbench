import { useEffect, useMemo, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { traceClient } from "./client";
import { buildTraceMetricsDiagnostics, traceMetricKinds } from "./trace-view-model";

const terminalTraceGracePollCount = 3;
const traceRefreshIntervalMs = 10000;
const terminalTraceGracePollIntervalMs = traceRefreshIntervalMs;
export function useWorkTrace({ projectId, agentWorkId, workCreatedAt = null, isWorkTerminal = false, isWorkLifecyclePending = false, workLifecycleGeneration = 0 }: {
  projectId?: string | null; agentWorkId?: string | null; workCreatedAt?: Date | null;
  isWorkTerminal?: boolean; isWorkLifecyclePending?: boolean; workLifecycleGeneration?: number;
}) {
  // A Work without a Project is read by its own identity.
  const enabled = Boolean(agentWorkId);
  const terminalKey = useRef<string | null>(null);
  const currentKey = `${agentWorkId}:${workLifecycleGeneration}`;
  const activeTraceSeenRef = useRef(false);
  const terminalGraceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const terminalGracePollsRemainingRef = useRef(0);
  const shouldQueryTrace = enabled && !isWorkLifecyclePending && !isWorkTerminal;
  const {
    data,
    error,
    isError,
    isLoading,
    isFetching,
    dataUpdatedAt,
    refetch,
  } = useQuery({
    queryKey: ["trace-metrics", projectId, agentWorkId, workCreatedAt?.getTime() ?? 0],
    queryFn: async () => {
      if (!agentWorkId) {
        return { buckets: [], totals: [] };
      }

      const metricWindow = workMetricsWindow(new Date(), workCreatedAt);
      return traceClient.getSessionTraceMetrics({
        // The trace API names a Work by its Work Caller; a Project id stands
        // for the Work Caller of that kind.
        workCaller: projectId ? { kind: "project", id: projectId } : undefined, agentWorkId,
        startTime: timestampFromDate(metricWindow.start),
        endTime: timestampFromDate(metricWindow.end),
        stepSeconds: metricWindow.stepSeconds,
        metricKinds: [...traceMetricKinds],
      });
    },
    enabled: shouldQueryTrace,
    retry: false,
    refetchInterval: shouldQueryTrace ? traceRefreshIntervalMs : false,
    refetchOnWindowFocus: false,
  });
  const latestRefetchRef = useRef(refetch);
  const latestTraceStateRef = useRef({
    enabled,
    agentWorkId: agentWorkId ?? null,
    isWorkLifecyclePending,
    isWorkTerminal,
  });
  const diagnostics = useMemo(() => buildTraceMetricsDiagnostics(data ?? { buckets: [], totals: [] }), [data]);
  const { summary } = diagnostics;

  useEffect(() => {
    latestRefetchRef.current = refetch;
    latestTraceStateRef.current = {
      enabled,
      agentWorkId: agentWorkId ?? null,
      isWorkLifecyclePending,
      isWorkTerminal,
    };
  }, [enabled, isWorkLifecyclePending, isWorkTerminal, refetch, agentWorkId]);

  useEffect(() => {
    return () => {
      if (terminalGraceTimerRef.current) {
        clearTimeout(terminalGraceTimerRef.current);
      }
    };
  }, []);

  useEffect(() => {
    activeTraceSeenRef.current = false;
    terminalGracePollsRemainingRef.current = 0;
    if (terminalGraceTimerRef.current) {
      clearTimeout(terminalGraceTimerRef.current);
      terminalGraceTimerRef.current = null;
    }
  }, [agentWorkId]);

  useEffect(() => {
    if (!shouldQueryTrace) {
      return;
    }

    activeTraceSeenRef.current = true;
    terminalKey.current = null;
    terminalGracePollsRemainingRef.current = 0;
    if (terminalGraceTimerRef.current) {
      clearTimeout(terminalGraceTimerRef.current);
      terminalGraceTimerRef.current = null;
    }
  }, [shouldQueryTrace]);

  useEffect(() => {
    if (!enabled || !agentWorkId || isWorkLifecyclePending || !isWorkTerminal || terminalKey.current === currentKey) {
      return;
    }

    const shouldGracePoll = activeTraceSeenRef.current;
    terminalKey.current = currentKey;
    if (isError) {
      return;
    }
    void refetch().then((result) => {
      if (!result.isError && shouldGracePoll) {
        scheduleTerminalGracePolling(latestRefetchRef, latestTraceStateRef, terminalGraceTimerRef, terminalGracePollsRemainingRef);
      }
    });
  }, [enabled, isError, isWorkLifecyclePending, isWorkTerminal, currentKey, refetch, agentWorkId]);

  return { data, error, isError, isLoading, isFetching, dataUpdatedAt, refetch, diagnostics, summary };
}

function scheduleTerminalGracePolling(
  latestRefetchRef: { current: () => Promise<{ isError: boolean }> },
  latestTraceStateRef: {
    current: {
      enabled: boolean;
      agentWorkId: string | null;
      isWorkLifecyclePending: boolean;
      isWorkTerminal: boolean;
    };
  },
  terminalGraceTimerRef: { current: ReturnType<typeof setTimeout> | null },
  terminalGracePollsRemainingRef: { current: number },
) {
  if (terminalGraceTimerRef.current) {
    clearTimeout(terminalGraceTimerRef.current);
  }

  terminalGracePollsRemainingRef.current = terminalTraceGracePollCount;
  const poll = () => {
    if (terminalGracePollsRemainingRef.current <= 0) {
      return;
    }

    terminalGraceTimerRef.current = setTimeout(() => {
      terminalGraceTimerRef.current = null;
      const state = latestTraceStateRef.current;
      if (!state.enabled || !state.agentWorkId || state.isWorkLifecyclePending || !state.isWorkTerminal) {
        terminalGracePollsRemainingRef.current = 0;
        return;
      }

      void latestRefetchRef.current().then((result) => {
        if (result.isError) {
          terminalGracePollsRemainingRef.current = 0;
          return;
        }

        terminalGracePollsRemainingRef.current -= 1;
        poll();
      });
    }, terminalTraceGracePollIntervalMs);
  };

  poll();
}

export function workMetricsWindow(now: Date, workCreatedAt: Date | null): { start: Date; end: Date; stepSeconds: number } {
  const end = new Date(now.getTime() + 60_000);
  const fallbackStart = new Date(now.getTime() - 30 * 24 * 60 * 60_000);
  const start = workCreatedAt && Number.isFinite(workCreatedAt.getTime())
    ? new Date(workCreatedAt.getTime() - 60_000)
    : fallbackStart;
  return { start, end, stepSeconds: 3600 };
}
