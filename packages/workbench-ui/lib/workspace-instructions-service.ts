import { ConnectError, Code } from "@connectrpc/connect";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCurrentUser } from "./account-service";
import { agentWorkClient } from "./client";
import { notify } from "./notifications";

// The Workspace Instructions of the signed-in person's Work Agent: what each of
// its Works places in its workspace as the instructions file at its next
// start, where anyone who can open the Work reads them.
export const MAX_WORKSPACE_INSTRUCTIONS = 16000;

const instructionsKey = (userId: string | undefined, agentId: string) => ["agent-workspace-instructions", userId, agentId];

/** Reads the Workspace Instructions; a Work Agent that does not exist yet has none. */
export function useWorkAgentWorkspaceInstructions(agentId: string | undefined) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: instructionsKey(userId, agentId ?? ""),
    queryFn: async () => agentId ? (await agentWorkClient.getAgentWorkspaceInstructions({ agentId })).instructions : "",
    enabled: Boolean(userId) && agentId !== undefined,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useChangeWorkAgentWorkspaceInstructions() {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: async ({ agentId, instructions }: { agentId: string; instructions: string }) => {
      // Saving is the first use: Agent establishes the Work Agent if it has none yet.
      const id = agentId || (await agentWorkClient.ensureMyWorkAgent({})).agentId;
      const response = await agentWorkClient.changeAgentWorkspaceInstructions({ agentId: id, instructions });
      return { agentId: id, instructions: response.instructions };
    },
    onSuccess: ({ agentId, instructions }, input) => {
      notify("work-agent-workspace-instructions", "success", "Workspace instructions saved", "Your Works use them from their next start.");
      queryClient.setQueryData(instructionsKey(userId, agentId), instructions);
      if (!input.agentId) void queryClient.invalidateQueries({ queryKey: ["my-work-agent", userId] });
    },
  });
}

export function workspaceInstructionsErrorMessage(error: unknown) {
  const code = error instanceof ConnectError ? error.code : undefined;
  if (code === Code.InvalidArgument) return `Use at most ${MAX_WORKSPACE_INSTRUCTIONS.toLocaleString("en-US")} characters.`;
  if (code === Code.PermissionDenied) return "Only your Work Agent's Owner can change its workspace instructions.";
  return "The workspace instructions could not be saved. Try again.";
}
