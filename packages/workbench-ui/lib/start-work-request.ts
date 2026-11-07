import type { JsonObject } from "@bufbuild/protobuf";
import type { PermissionMode } from "@talgent/contracts/agent/public/v1/harness_pb";

/** What the person chose for a new Work, whatever the Work is for. */
export interface WorkStartInput {
  initialMessage: string;
  /** The selected Model Configuration, or the Managed Computer's own Claude
   * login; absence leaves it to the platform's default. */
  model?: { configurationId?: string; machineLogin?: boolean };
  thinkingLevel?: string;
  creationCapabilities?: {
    plugins: Array<{ name: string; include: boolean; configuration: JsonObject }>;
    connectors: Array<{ name: string; include: boolean }>;
    startupAgent?: string;
  };
  repositorySelections?: Array<{ repositoryId: string; baseBranch: string }>;
  /** The Computer the Work runs on for its life; absence leaves it to the platform's default Computer. */
  computerId?: string;
  /** How the Harness runs on a Managed Computer; absent for a Cloud. */
  harness?: { permissionMode: PermissionMode; sandbox: boolean };
}

/** The start request's fields for the choices, leaving unchosen ones unset. */
export function workStartFields(input: WorkStartInput) {
  return {
    initialMessage: input.initialMessage,
    repositorySelections: input.repositorySelections ?? [],
    ...(input.model && { model: input.model }),
    ...(input.thinkingLevel !== undefined && { thinkingLevel: input.thinkingLevel }),
    ...(input.creationCapabilities && { creationCapabilities: input.creationCapabilities }),
    ...(input.computerId && { computerId: input.computerId }),
    ...(input.harness && { harness: input.harness }),
  };
}
