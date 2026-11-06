import { useQuery } from "@tanstack/react-query";

// The platform release this deployment runs, as Computer's machine service
// answers it (GET /computer/v1/release): Talgent Computer is published to npm at that
// version. Empty when the deployment is no release.

const release = /^\d+\.\d+\.\d+$/;

export async function fetchComputerRelease(): Promise<string> {
  const response = await fetch("/computer/v1/release", { headers: { Accept: "application/json" } });
  if (!response.ok) throw new Error(`the release answered ${response.status}`);
  const body: unknown = await response.json();
  const version = typeof body === "object" && body !== null && "version" in body ? body.version : undefined;
  return typeof version === "string" && release.test(version) ? version : "";
}

export function useComputerRelease() {
  return useQuery({ queryKey: ["computer-release"], queryFn: fetchComputerRelease, staleTime: 5 * 60_000 });
}

/** The commands that install Talgent Computer on a machine of any platform,
 * at the release this deployment runs, register it with this entry and keep
 * it running as the person's background service. */
export function installCommands(origin: string, version: string): string[] {
  return [
    `npm install -g @talgent-work/computer${version === "" ? "" : `@${version}`}`,
    `talgent-computer register --server ${origin}`,
    "talgent-computer service install",
  ];
}
