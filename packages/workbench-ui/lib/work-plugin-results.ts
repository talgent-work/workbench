import { useQuery } from "@tanstack/react-query";
import { agentWorkClient } from "./client";

// The Work's Plugin preparation results, shared by the composer's degradation
// notices and the context card. Only the running composer polls; every reader
// sees the same cache.
export function useWorkPluginResults(workId: string | undefined, poll = false) {
  return useQuery({ queryKey: ["work-plugin-results", workId], queryFn: () => agentWorkClient.getWorkPluginResults({ workId }), enabled: Boolean(workId), retry: false, refetchInterval: poll ? 30_000 : false });
}
