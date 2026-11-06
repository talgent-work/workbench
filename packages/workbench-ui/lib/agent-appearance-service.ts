import { ConnectError, Code } from "@connectrpc/connect";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCurrentUser } from "./account-service";
import { agentWorkClient } from "./client";
import { notify } from "./notifications";
import { agentIconOf, type AgentIconValue } from "./agent-icon";

// The signed-in person's Work Agent: its name and icon are theirs to set.
// Before it exists, agentId is empty and the appearance is the one it would
// start with.
export interface MyWorkAgent { agentId: string; name: string; icon: AgentIconValue }

/** The meta of a query whose data shows the person's Work Agent, such as a
 * thread naming its replies' author: saving the Agent's appearance refreshes it. */
export const showsWorkAgent = { showsWorkAgent: true };

const myWorkAgentKey = (userId: string | undefined) => ["my-work-agent", userId];

export function useMyWorkAgent() {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: myWorkAgentKey(userId),
    queryFn: async (): Promise<MyWorkAgent> => {
      const agent = await agentWorkClient.getMyWorkAgent({});
      return { agentId: agent.agentId, name: agent.appearance?.name ?? "", icon: agentIconOf(agent.appearance?.icon) };
    },
    enabled: Boolean(userId),
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useChangeMyWorkAgentAppearance() {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: async (input: MyWorkAgent): Promise<MyWorkAgent> => {
      // Saving is the first use: Agent establishes the Work Agent if it has none yet.
      const agentId = input.agentId || (await agentWorkClient.ensureMyWorkAgent({})).agentId;
      const response = await agentWorkClient.changeWorkAgentAppearance({ agentId, name: input.name, icon: input.icon });
      return { agentId, name: response.appearance?.name ?? input.name, icon: agentIconOf(response.appearance?.icon ?? input.icon) };
    },
    onSuccess: agent => {
      // Saving remounts the editor with the saved appearance, so the notice
      // belongs to the mutation rather than the form.
      notify("work-agent-appearance", "success", "Work Agent saved");
      queryClient.setQueryData(myWorkAgentKey(userId), agent);
      // Every place the Agent appears reads its current appearance.
      void queryClient.invalidateQueries({ predicate: query => query.meta?.showsWorkAgent === true });
    },
  });
}

export function agentAppearanceErrorMessage(error: unknown) {
  const code = error instanceof ConnectError ? error.code : undefined;
  if (code === Code.InvalidArgument) return "Use a name of 1–40 characters and one of the listed icons and colors.";
  if (code === Code.PermissionDenied) return "Only the person a Work Agent represents can change it.";
  return "Your Work Agent could not be saved. Try again.";
}
