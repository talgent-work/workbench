import { ConnectError, Code } from "@connectrpc/connect";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { GetWorkStartOptionsResponse } from "@talgent/contracts/agent/public/v1/work_start_options_pb";
import { useCurrentUser } from "./account-service";
import { agentIconOf, type AgentAppearanceValue } from "./agent-icon";
import { agentWorkClient } from "./client";
import { notify } from "./notifications";

// An Application's Agent as its Application Owner configures it, under their
// own sign-in: its appearance, its Soul and the Work defaults its Works start
// with when their start request names none. Agent establishes it on its first
// need, named after the Application.

export type ApplicationAgent = AgentAppearanceValue & { agentId: string };

/** What a Work of the Application starts with when its start names none. */
export type WorkDefaults = { startupAgent: string; modelConfigurationId: string; thinkingLevel: string };

export type AgentWorkDefaults = { defaults: WorkDefaults; options?: GetWorkStartOptionsResponse };

const agentKey = (userId: string | undefined, applicationId: string) => ["application-agent", userId, applicationId];
const soulKey = (userId: string | undefined, agentId: string) => ["application-agent", userId, "soul", agentId];
const defaultsKey = (userId: string | undefined, agentId: string) => ["application-agent", userId, "work-defaults", agentId];

export function useApplicationAgent(applicationId: string) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: agentKey(userId, applicationId),
    queryFn: async (): Promise<ApplicationAgent> => {
      const agent = await agentWorkClient.ensureApplicationAgent({ applicationId });
      return { agentId: agent.agentId, name: agent.appearance?.name ?? "", icon: agentIconOf(agent.appearance?.icon) };
    },
    enabled: Boolean(userId && applicationId),
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useChangeApplicationAgentAppearance(applicationId: string) {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: async ({ agentId, name, icon }: ApplicationAgent): Promise<ApplicationAgent> => {
      const response = await agentWorkClient.changeWorkAgentAppearance({ agentId, name, icon });
      return { agentId, name: response.appearance?.name ?? name, icon: agentIconOf(response.appearance?.icon ?? icon) };
    },
    onSuccess: agent => {
      notify("application-agent-appearance", "success", "Agent saved");
      queryClient.setQueryData(agentKey(userId, applicationId), agent);
      void queryClient.invalidateQueries({ predicate: query => query.meta?.showsWorkAgent === true });
    },
  });
}

export function useApplicationAgentSoul(agentId: string | undefined) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: soulKey(userId, agentId ?? ""),
    queryFn: async () => (await agentWorkClient.getAgentSoul({ agentId: agentId! })).soul,
    enabled: Boolean(userId && agentId),
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useChangeApplicationAgentSoul(agentId: string) {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: async (soul: string) => (await agentWorkClient.changeAgentSoul({ agentId, soul })).soul,
    onSuccess: soul => {
      notify("application-agent-soul", "success", "Soul saved", "The application's Works take it up at their next start.");
      queryClient.setQueryData(soulKey(userId, agentId), soul);
    },
  });
}

const defaultsOf = (value?: { startupAgent: string; modelConfigurationId: string; thinkingLevel: string }): WorkDefaults =>
  ({ startupAgent: value?.startupAgent ?? "", modelConfigurationId: value?.modelConfigurationId ?? "", thinkingLevel: value?.thinkingLevel ?? "" });

export function useAgentWorkDefaults(agentId: string | undefined) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: defaultsKey(userId, agentId ?? ""),
    queryFn: async (): Promise<AgentWorkDefaults> => {
      const response = await agentWorkClient.getAgentWorkDefaults({ agentId: agentId! });
      return { defaults: defaultsOf(response.defaults), options: response.options };
    },
    enabled: Boolean(userId && agentId),
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useChangeAgentWorkDefaults(agentId: string) {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: async (defaults: WorkDefaults) => defaultsOf((await agentWorkClient.changeAgentWorkDefaults({ agentId, defaults })).defaults),
    onSuccess: () => {
      notify("application-work-defaults", "success", "Work defaults saved", "Works started from now on use them.");
      void queryClient.invalidateQueries({ queryKey: defaultsKey(userId, agentId) });
    },
    // A model withdrawn meanwhile is refused; reread what is offered now.
    onError: () => void queryClient.invalidateQueries({ queryKey: defaultsKey(userId, agentId) }),
  });
}

export function applicationAgentErrorMessage(error: unknown, subject: string) {
  const code = error instanceof ConnectError ? error.code : undefined;
  if (code === Code.InvalidArgument) return `That ${subject} isn't valid any more. Check it and try again.`;
  if (code === Code.PermissionDenied) return "Only the application's owner can change its Agent.";
  return `The ${subject} could not be saved. Try again.`;
}
