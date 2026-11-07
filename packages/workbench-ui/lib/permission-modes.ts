import { PermissionMode } from "@talgent/contracts/agent/public/v1/harness_pb";

// How much a Managed Work's Harness asks before it acts, as the person picks
// it at start and while the Work runs. A Cloud Work has no permission mode.
export type PermissionModeOption = { mode: PermissionMode; name: string; short: string; description: string };

export const permissionModes: PermissionModeOption[] = [
  { mode: PermissionMode.ASK, name: "Ask", short: "Ask", description: "Asks before each action its rules don’t allow" },
  { mode: PermissionMode.ACCEPT_EDITS, name: "Accept edits", short: "Accept edits", description: "Edits files without asking" },
  { mode: PermissionMode.PLAN, name: "Plan", short: "Plan", description: "Changes nothing until its plan is approved" },
  { mode: PermissionMode.BYPASS_PERMISSIONS, name: "Bypass permissions", short: "Bypass", description: "Acts without asking" },
];

// Modes the Harness may report it switched to by itself; no one picks them.
const reportedPermissionModes: PermissionModeOption[] = [
  { mode: PermissionMode.DONT_ASK, name: "Don’t ask", short: "Don’t ask", description: "Denies what its rules don’t allow, without asking" },
  { mode: PermissionMode.AUTO, name: "Auto", short: "Auto", description: "Decides by itself what to ask" },
];

/** A Managed start's default: the Harness acts without asking. */
export const defaultPermissionMode = PermissionMode.BYPASS_PERMISSIONS;

export function permissionModeOption(mode: PermissionMode) {
  return permissionModes.find(option => option.mode === mode) ?? reportedPermissionModes.find(option => option.mode === mode);
}
