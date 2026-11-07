import { useRef, useState } from "react";
import type { WorkInfo } from "@talgent/contracts/agent/public/v1/work_pb";
import { agentWorkClient } from "../lib/client";
import { isStaleWorkControl, workControlErrorMessage } from "../lib/work-projection";

// Every control names the Work version the user saw, except resuming with
// the owner's own message. onSettled refreshes the authority, so a rejected
// stale control is followed by the current state and the user confirms again
// against it. Any other refusal describes the version it was raised against,
// so it is withdrawn once the Work moves on; a refused resume after a message
// stays until the next action, since that message already moved the Work on.
export function useWorkLifecycleControls(workId: string | null | undefined, onWorkChange: (work?: WorkInfo) => void, onSettled: () => void, expectedVersion?: bigint) {
  const inFlight = useRef(false);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<{ message: string; version?: bigint }>();
  const [archiveOpen, setArchiveOpen] = useState(false);
  const run = async (action: () => Promise<void>, versioned = true) => {
    if (!workId || inFlight.current) return;
    inFlight.current = true; setPending(true); setFailure(undefined);
    try { await action(); }
    catch (cause) {
      const stale = isStaleWorkControl(cause);
      setFailure({ message: workControlErrorMessage(cause), version: stale || !versioned ? undefined : expectedVersion });
      // The confirmation was given for a state that no longer holds.
      if (stale) setArchiveOpen(false);
    }
    finally { inFlight.current = false; setPending(false); onSettled(); }
  };
  const error = failure && (failure.version === undefined || failure.version === expectedVersion) ? failure.message : "";
  const request = () => ({ workId: workId!, expectedVersion });
  const stop = () => run(async () => onWorkChange((await agentWorkClient.stopWork(request())).work));
  const resume = () => run(async () => onWorkChange((await agentWorkClient.resumeWork(request())).work));
  // Resuming after accepted input is the caller's decision. The
  // owner's message itself moved the Work on, so the resume that continues it
  // names no version; the Work's state still decides.
  const continueWithInput = () => run(async () => onWorkChange((await agentWorkClient.resumeWork({ workId: workId! })).work), false);
  const restart = () => run(async () => onWorkChange((await agentWorkClient.restartWork(request())).work));
  const archive = () => run(async () => { onWorkChange((await agentWorkClient.archiveWork(request())).work); setArchiveOpen(false); });
  return { pending, error, archiveOpen, setArchiveOpen, run, stop, resume, continueWithInput, restart, archive };
}
