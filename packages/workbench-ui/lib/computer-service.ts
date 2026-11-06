import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AdmissionPolicy as AdmissionPolicyMessage, UsableComputerKind, type ManagedComputer as ManagedComputerMessage } from "@talgent-work/workbench-contracts/computer/public/v1/computer_pb";
import { useCurrentUser } from "./account-service";
import { computerClient } from "./client";
import { timeAgo } from "./relative-time";

// The person's own Managed Computers (Computer's ComputerService): the
// machines they registered, whether the platform reaches each now, naming
// them, and approving a machine's registration. A Cloud never appears here.

export type Presence = { kind: "online" } | { kind: "offline"; since: Date | null } | { kind: "never" };

/** What a full Computer answers a run: reject it, or queue it. */
export type AdmissionPolicy = "reject" | "queue";

export type ManagedComputer = {
  id: string;
  name: string;
  presence: Presence;
  talgentComputerVersion: string;
  claudeCodeVersion: string;
  /** How many Works it runs at once. */
  capacity: number;
  policy: AdmissionPolicy;
  /** How many Works it runs now, and how many wait in its line. */
  running: number;
  waiting: number;
};

function presenceOf(presence: ManagedComputerMessage["presence"]): Presence {
  if (presence?.reachable) return { kind: "online" };
  if (!presence || presence.neverConnected) return { kind: "never" };
  const since = Number(presence.unreachableSinceUnixMs);
  return { kind: "offline", since: since > 0 ? new Date(since) : null };
}

export function managedComputer(computer: ManagedComputerMessage): ManagedComputer {
  return {
    id: computer.id,
    name: computer.name,
    presence: presenceOf(computer.presence),
    talgentComputerVersion: computer.talgentComputerVersion,
    claudeCodeVersion: computer.claudeCodeVersion,
    capacity: Number(computer.capacity),
    policy: computer.admissionPolicy === AdmissionPolicyMessage.QUEUE ? "queue" : "reject",
    running: Number(computer.running),
    waiting: Number(computer.waiting),
  };
}

/** How a Computer's room reads in the list ("1 of 2 running · 1 waiting"). */
export function roomLabel(computer: Pick<ManagedComputer, "capacity" | "running" | "waiting">): string {
  const running = `${computer.running} of ${computer.capacity} running`;
  return computer.waiting > 0 ? `${running} · ${computer.waiting} waiting` : running;
}

export const maxCapacity = 32;

/** Why a Capacity can't be saved, or "" when it can. */
export function capacityProblem(capacity: number, running: number): string {
  if (!Number.isInteger(capacity) || capacity < 1 || capacity > maxCapacity) return `Choose 1 to ${maxCapacity} Works.`;
  if (capacity < running) return `It runs ${running} Works now. Choose at least ${running}.`;
  return "";
}

/** How a Computer's Presence reads in the list. */
export function presenceLabel(presence: Presence, now = Date.now()): string {
  switch (presence.kind) {
    case "online": return "Online";
    case "never": return "Never connected";
    case "offline": return presence.since ? `Offline since ${timeAgo(presence.since, now)}` : "Offline";
  }
}

export const maxComputerName = 64;

/** Why a name can't be saved, or "" when it can. The server trims too. */
export function computerNameProblem(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return "Enter a name.";
  if (Array.from(trimmed).length > maxComputerName) return `Use at most ${maxComputerName} characters.`;
  return "";
}

export const computersKey = ["computers"] as const;

/** The person's Managed Computers, newest first, polled so Presence moves
 * without a reload. */
export function useComputers() {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: [...computersKey, userId ?? ""],
    queryFn: async ({ signal }) => (await computerClient.listComputers({}, { signal })).computers.map(managedComputer),
    enabled: Boolean(userId),
    retry: false,
    refetchInterval: 10_000,
    refetchOnWindowFocus: false,
  });
}

/** A Computer a person may start a Personal Work on: a Cloud, or one of
 * their Managed Computers, which takes a start only while reachable. sandbox
 * is whether a Managed Computer's machine reports it can provide Claude
 * Code's sandbox. */
export type UsableComputer = { id: string; name: string; managed: boolean; reachable: boolean; sandbox: boolean };

/** Every Cloud, then the person's Managed Computers; the first is the default.
 * Polled so a Managed Computer going offline shows before a start is refused. */
export function useUsableComputers(enabled = true) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: ["usable-computers", userId ?? ""],
    queryFn: async ({ signal }): Promise<UsableComputer[]> => (await computerClient.listUsableComputers({}, { signal })).computers.map(computer => ({
      id: computer.id,
      name: computer.name,
      managed: computer.kind === UsableComputerKind.MANAGED,
      reachable: computer.kind !== UsableComputerKind.MANAGED || computer.reachable,
      sandbox: computer.kind === UsableComputerKind.MANAGED && computer.claudeCodeSandbox,
    })),
    enabled: enabled && Boolean(userId),
    retry: false,
    refetchInterval: 10_000,
    refetchOnWindowFocus: false,
  });
}

/** The Computer a Work runs on, as its reader sees it: named when it is one
 * the reader may use; another person's Managed Computer is shown only as
 * managed. Absent until the Work names its Computer. */
export type WorkComputer = { name?: string; managed: boolean };

export function useWorkComputer(computerId: string | undefined, managed: boolean): WorkComputer | undefined {
  const usable = useUsableComputers(Boolean(computerId));
  if (!computerId) return undefined;
  const known = usable.data?.find(computer => computer.id === computerId);
  return known ? { name: known.name, managed: known.managed } : { managed };
}

export function useRenameComputer(computerId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (name: string) => managedComputer((await computerClient.renameComputer({ computerId, name: name.trim() })).computer!),
    onSuccess: () => { void client.invalidateQueries({ queryKey: computersKey }); },
  });
}

export function useSetRoomRules(computerId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ capacity, policy }: { capacity: number; policy: AdmissionPolicy }) => managedComputer((await computerClient.setRoomRules({
      computerId, capacity: BigInt(capacity), admissionPolicy: policy === "queue" ? AdmissionPolicyMessage.QUEUE : AdmissionPolicyMessage.REJECT,
    })).computer!),
    onSuccess: () => { void client.invalidateQueries({ queryKey: computersKey }); },
  });
}

/** Removes one of the person's Managed Computers for good. The lists of
 * Computers, and of those a Work may start on, no longer show it, nor one
 * that no longer exists. */
export function useRemoveComputer(computerId: string) {
  const client = useQueryClient();
  const refresh = () => {
    void client.invalidateQueries({ queryKey: computersKey });
    void client.invalidateQueries({ queryKey: ["usable-computers"] });
  };
  return useMutation({
    mutationFn: async () => { await computerClient.removeComputer({ computerId }); },
    onSuccess: refresh,
    onError: (error) => { if (ConnectError.from(error).code === Code.NotFound) refresh(); },
  });
}

/** Why a Computer wasn't removed, as the person acts on it. */
export function removeComputerErrorMessage(error: unknown): string {
  if (ConnectError.from(error).code === Code.NotFound) return "This computer no longer exists.";
  return "The computer couldn’t be removed. Try again.";
}

/** Why room rules weren't saved, as the person acts on it. */
export function roomRulesErrorMessage(error: unknown): string {
  const code = ConnectError.from(error).code;
  if (code === Code.FailedPrecondition) return "It runs more Works now than that Capacity. Choose a higher Capacity or try again once some end.";
  if (code === Code.NotFound) return "This computer no longer exists.";
  return "The settings couldn’t be saved. Try again.";
}

/** A user code as Talgent Computer shows it ("ABCD-EFGH"), from what the
 * person typed or the link carried. */
export function normalizeUserCode(value: string | null | undefined): string {
  const compact = (value ?? "").replace(/[^0-9a-z]/gi, "").toUpperCase();
  return compact.length > 4 ? `${compact.slice(0, 4)}-${compact.slice(4)}` : compact;
}

export type RegistrationRefusal = "invalid" | "taken" | "failed";

/** Why reading or approving a registration failed, as the person acts on it. */
export function registrationRefusal(error: unknown): RegistrationRefusal {
  const code = ConnectError.from(error).code;
  if (code === Code.NotFound) return "invalid";
  if (code === Code.FailedPrecondition) return "taken";
  return "failed";
}

export const registrationRefusalText: Record<RegistrationRefusal, string> = {
  invalid: "This code is invalid or expired",
  taken: "Another person already approved this code",
  failed: "This code couldn’t be checked",
};

export type Registration = { machineName: string; expiresAt: Date | null; approved: boolean };

export function useRegistration(userCode: string) {
  return useQuery({
    queryKey: ["computer-registration", userCode],
    queryFn: async ({ signal }): Promise<Registration> => {
      const response = await computerClient.getRegistration({ userCode }, { signal });
      const expires = Number(response.expiresAtUnixMs);
      return { machineName: response.machineName, expiresAt: expires > 0 ? new Date(expires) : null, approved: response.approved };
    },
    enabled: userCode.length > 0,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

export function useApproveRegistration() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (userCode: string) => { await computerClient.approveRegistration({ userCode }); },
    onSuccess: () => { void client.invalidateQueries({ queryKey: computersKey }); },
  });
}

/** How long a pending registration has left ("Expires in 9m"). */
export function expiryLabel(expiresAt: Date | null, now = Date.now()): string {
  if (!expiresAt) return "";
  const minutes = Math.ceil((expiresAt.getTime() - now) / 60_000);
  if (minutes <= 0) return "Expired";
  return minutes < 60 ? `Expires in ${minutes}m` : `Expires in ${Math.floor(minutes / 60)}h`;
}
