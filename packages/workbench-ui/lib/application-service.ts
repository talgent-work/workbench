import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ClientSecretState, type Application as ApplicationMessage } from "@talgent/contracts/account/public/v1/application_pb";
import { useCurrentUser } from "./account-service";
import { applicationClient } from "./client";

// The person's own Applications (Account's ApplicationService): programs that
// use Workbench through its public APIs as themselves. Their owner registers
// them, receives each Client Secret once and reads them here; nobody else
// sees them.

export type ClientSecret = {
  id: string;
  state: "current" | "overlapping";
  /** The secret's last characters, so its owner can tell secrets apart. */
  hint: string;
  issuedAt: Date;
  lastUsedAt: Date | null;
  overlapEndsAt: Date | null;
};

export type Application = {
  /** Also the OAuth client id. */
  id: string;
  name: string;
  registeredAt: Date;
  secrets: ClientSecret[];
};

const at = (unixMs: bigint) => unixMs > 0n ? new Date(Number(unixMs)) : null;

export function applicationOf(message: ApplicationMessage): Application {
  return {
    id: message.id,
    name: message.name,
    registeredAt: at(message.registeredAtUnixMs) ?? new Date(0),
    secrets: message.clientSecrets.map(secret => ({
      id: secret.id,
      state: secret.state === ClientSecretState.OVERLAPPING ? "overlapping" : "current",
      hint: secret.hint,
      issuedAt: at(secret.issuedAtUnixMs) ?? new Date(0),
      lastUsedAt: at(secret.lastUsedAtUnixMs),
      overlapEndsAt: at(secret.overlapEndsAtUnixMs),
    })),
  };
}

/** How a Client Secret reads, from the prefix every secret starts with. */
export const secretLabel = (secret: Pick<ClientSecret, "hint">) => `wbs_…${secret.hint}`;

export const maxApplicationName = 60;

/** Why a name can't be registered, or "" when it can. The server trims too. */
export function applicationNameProblem(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "Enter a name.";
  if ([...trimmed].length > maxApplicationName) return `Use at most ${maxApplicationName} characters.`;
  return "";
}

const ownedApplicationsKey = (userId: string | undefined) => ["account", "applications", userId];

export function useOwnedApplications() {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: ownedApplicationsKey(userId),
    queryFn: async () => (await applicationClient.listOwnedApplications({})).applications.map(applicationOf),
    enabled: Boolean(userId),
  });
}

/** A newly registered Application with the Client Secret only this response shows. */
export type RegisteredApplication = { application: Application; clientSecret: string };

export function useRegisterApplication() {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: async (name: string): Promise<RegisteredApplication> => {
      const response = await applicationClient.registerApplication({ name: name.trim() });
      if (!response.application) throw new Error("The registered application is missing from the response.");
      return { application: applicationOf(response.application), clientSecret: response.clientSecret };
    },
    onSuccess: ({ application }) => {
      queryClient.setQueryData<Application[]>(ownedApplicationsKey(userId), current => current && !current.some(item => item.id === application.id) ? [...current, application] : current);
      void queryClient.invalidateQueries({ queryKey: ownedApplicationsKey(userId) });
    },
  });
}
