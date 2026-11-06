import { ConnectError, Code } from "@connectrpc/connect";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCurrentUser } from "./account-service";
import { agentWorkClient } from "./client";
import { notify } from "./notifications";

// The Soul of the signed-in person's Work Agent: the persona and working style
// every one of its Works takes up at its next start. Only its Owner reads it.
export const MAX_SOUL = 8000;

const soulKey = (userId: string | undefined, agentId: string) => ["agent-soul", userId, agentId];

/** Reads the Soul; a Work Agent that does not exist yet has none. */
export function useWorkAgentSoul(agentId: string | undefined) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: soulKey(userId, agentId ?? ""),
    queryFn: async () => agentId ? (await agentWorkClient.getAgentSoul({ agentId })).soul : "",
    enabled: Boolean(userId) && agentId !== undefined,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useChangeWorkAgentSoul() {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: async ({ agentId, soul }: { agentId: string; soul: string }) => {
      // Saving is the first use: Agent establishes the Work Agent if it has none yet.
      const id = agentId || (await agentWorkClient.ensureMyWorkAgent({})).agentId;
      const response = await agentWorkClient.changeAgentSoul({ agentId: id, soul });
      return { agentId: id, soul: response.soul };
    },
    onSuccess: ({ agentId, soul }, input) => {
      notify("work-agent-soul", "success", "Soul saved", "Your Works take it up at their next start.");
      queryClient.setQueryData(soulKey(userId, agentId), soul);
      if (!input.agentId) void queryClient.invalidateQueries({ queryKey: ["my-work-agent", userId] });
    },
  });
}

export function soulErrorMessage(error: unknown) {
  const code = error instanceof ConnectError ? error.code : undefined;
  if (code === Code.InvalidArgument) return `Use at most ${MAX_SOUL} characters.`;
  if (code === Code.PermissionDenied) return "Only your Work Agent's Owner can change its Soul.";
  return "The Soul could not be saved. Try again.";
}
