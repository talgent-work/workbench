import { useQuery } from "@tanstack/react-query";
import { agentWorkClient } from "./client";

// A Work as Agent shows it to its owner (AgentWorkService.GetWork). Every
// reader of one Work shares this cache entry, which holds Agent's response.
export const agentWorkQueryKey = (workId: string | null | undefined) => ["agent-work", workId] as const;

/** Polls a Work's state while it starts or runs; a closed Work no longer changes. */
export function workPollInterval(data: { work?: { state: string } } | undefined) {
  return data?.work?.state === "closed" ? false : 5000;
}

/** The owner's view of the Work; Agent refuses anyone else. */
export function useAgentWork(workId: string | null | undefined, enabled = true) {
  return useQuery({
    queryKey: agentWorkQueryKey(workId),
    queryFn: ({ signal }) => agentWorkClient.getWork({ workId: workId! }, { signal }),
    enabled: Boolean(workId) && enabled,
    refetchInterval: query => workPollInterval(query.state.data),
    retry: false,
    refetchOnWindowFocus: false,
  });
}
