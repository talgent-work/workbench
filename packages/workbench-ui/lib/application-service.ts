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
  /** How long a replaced secret keeps working after a rotation. */
  secretOverlapMs: number;
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
    secretOverlapMs: Number(message.secretOverlapSeconds) * 1000,
  };
}

/** How a Client Secret reads, from the prefix every secret starts with. */
export const secretLabel = (secret: Pick<ClientSecret, "hint">) => `wbs_…${secret.hint}`;

/** The secrets that still match at now: an overlap that ended since the list
 * was read no longer counts. */
export const liveSecrets = (application: Application, now: number) =>
  application.secrets.filter(secret => !secret.overlapEndsAt || secret.overlapEndsAt.getTime() > now);

/** A rotation overlap in words, such as "24 hours". */
export function overlapText(ms: number): string {
  const hours = ms / 3_600_000;
  if (Number.isInteger(hours)) return hours === 1 ? "1 hour" : `${hours} hours`;
  const minutes = Math.round(ms / 60_000);
  return minutes === 1 ? "1 minute" : `${minutes} minutes`;
}

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

/** Puts the Application a change answered with into the owner's list, or
 * rereads the list after a change failed. */
function useOwnedApplicationUpdates() {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ownedApplicationsKey(userId) });
  const replace = (application: Application) => {
    queryClient.setQueryData<Application[]>(ownedApplicationsKey(userId), current => current?.map(item => item.id === application.id ? application : item));
    refresh();
  };
  return { replace, refresh };
}

/** A rotated Application with the new Client Secret only this response shows. */
export type RotatedSecret = { application: Application; clientSecret: string };

export function useRotateClientSecret() {
  const { replace, refresh } = useOwnedApplicationUpdates();
  return useMutation({
    mutationFn: async (applicationId: string): Promise<RotatedSecret> => {
      const response = await applicationClient.rotateClientSecret({ applicationId });
      if (!response.application) throw new Error("The rotated application is missing from the response.");
      return { application: applicationOf(response.application), clientSecret: response.clientSecret };
    },
    onSuccess: ({ application }) => replace(application),
    onError: refresh,
  });
}

export function useRevokeClientSecret() {
  const { replace, refresh } = useOwnedApplicationUpdates();
  return useMutation({
    mutationFn: async ({ applicationId, clientSecretId }: { applicationId: string; clientSecretId: string }): Promise<Application> => {
      const response = await applicationClient.revokeClientSecret({ applicationId, clientSecretId });
      if (!response.application) throw new Error("The application is missing from the response.");
      return applicationOf(response.application);
    },
    onSuccess: replace,
    // A secret that ended meanwhile answers NotFound; the list shows why.
    onError: refresh,
  });
}

/** Removes an Application for good: its secrets stop at once and its open
 * Works close. It leaves the owner's list, which a failure rereads. */
export function useRemoveApplication() {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  const { refresh } = useOwnedApplicationUpdates();
  return useMutation({
    mutationFn: async (applicationId: string) => { await applicationClient.removeApplication({ applicationId }); return applicationId; },
    onSuccess: applicationId => {
      queryClient.setQueryData<Application[]>(ownedApplicationsKey(userId), current => current?.filter(item => item.id !== applicationId));
      refresh();
    },
    // An Application removed meanwhile answers NotFound; the list shows it gone.
    onError: refresh,
  });
}
