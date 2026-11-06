import { Code, ConnectError } from "@connectrpc/connect";
import { expect, it } from "vitest";
import { computerOfflineMessage, inputPendingMessage, staleWorkControlMessage, withdrawnModelMessage, workControlErrorMessage } from "../lib/work-projection";

it("explains a Resume or Restart refused because the Work's model is withdrawn", () => {
  const withdrawn = new ConnectError("agent: the Work's model configuration is withdrawn", Code.FailedPrecondition);
  expect(workControlErrorMessage(withdrawn)).toBe(withdrawnModelMessage);
  expect(workControlErrorMessage(new ConnectError("stale", Code.Aborted))).toBe(staleWorkControlMessage);
  expect(workControlErrorMessage(new ConnectError("capacity exhausted", Code.ResourceExhausted))).toContain("capacity exhausted");
});

it("explains a start refused because the Work's Computer is offline, without Agent's error", () => {
  const unreachable = new ConnectError("agent: the Computer is unreachable", Code.FailedPrecondition);
  expect(workControlErrorMessage(unreachable)).toBe(computerOfflineMessage);
});

it("explains an archive refused because the Work still has accepted input", () => {
  const pending = new ConnectError("agent: work transition not allowed: the Work still has accepted input to finish", Code.FailedPrecondition);
  expect(workControlErrorMessage(pending)).toBe(inputPendingMessage);
});
