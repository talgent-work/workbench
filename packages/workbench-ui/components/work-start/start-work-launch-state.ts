import { Code, ConnectError } from "@connectrpc/connect";
import { ComputerOutdatedSchema, SandboxUnavailableSchema } from "@talgent/contracts/agent/public/v1/harness_pb";

/** Why a start failed; computer is the one it was started on. A full Managed
 * Computer, one without Claude Code's sandbox, or one below the Minimum
 * Version is named with what its owner can do about it. */
export function startWorkErrorMessage(err: unknown, computer?: { name: string; managed: boolean }) {
  if (err instanceof ConnectError && err.code === Code.ResourceExhausted) {
    if (computer?.managed) return `${computer.name} already runs as many Works as its Capacity allows. Try again once one ends, or raise its Capacity or let it queue runs on the Computers page in your account menu.`;
    return "Execution capacity is unavailable. Try starting again when capacity is free.";
  }
  const message = err instanceof Error ? err.message : "Failed to start work.";
  if (err instanceof ConnectError && err.findDetails(SandboxUnavailableSchema).length > 0) {
    return `${computer?.name ?? "This Computer"} can’t run Works in Claude Code’s sandbox (native Windows has none; on Linux install bubblewrap and socat). Turn the sandbox off or choose another Computer.`;
  }
  if (err instanceof ConnectError && err.findDetails(ComputerOutdatedSchema).length > 0) {
    return `${computer?.name ?? "This Computer"} runs a Talgent Computer older than Talgent supports, so it takes no new Works. It upgrades itself while connected; if it stays outdated, reinstall it with the command on the Computers page in your account menu.`;
  }
  if (/github_(reconnect_required|authorization_revoked)/i.test(message)) {
    return "GitHub authorization needs to be renewed in Marketplace › Providers before repository checkout can start.";
  }
  if (err instanceof ConnectError && err.code === Code.FailedPrecondition && isRepositoryCheckoutPreconditionMessage(message)) {
    return `GitHub repository checkout cannot start (failed_precondition): ${message}`;
  }
  if (/\[failed_precondition\]/i.test(message) && isRepositoryCheckoutPreconditionMessage(message)) {
    return `GitHub repository checkout cannot start: ${message}`;
  }
  return message;
}

function isRepositoryCheckoutPreconditionMessage(message: string) {
  return /(scm|github|checkout|repository)/i.test(message);
}
