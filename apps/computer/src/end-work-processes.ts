import type { ProcessTable } from './execd/processes.ts'

// How often ending the Work processes is tried before the run gives up.
const ATTEMPTS = 3

// Why the platform can never reach this machine's Work processes again.
export type FinalDisconnection = 'computer_removed' | 'credential_rejected'

const WHY: Record<FinalDisconnection, string> = {
  computer_removed: 'this Computer was removed',
  credential_rejected: 'Talgent refuses this Computer\'s credential',
}

// Ends every Work process on this machine once the platform can never reach
// it again: its Computer was removed, or Talgent refuses its credential for
// good. The platform stops the Works for Disconnection and never attaches to
// them again, and this run exits, so its fence no longer ends them: nothing
// may keep running on the platform's behalf. Starts are held meanwhile, so
// no process launched while the list is taken is missed. Processes stay
// known with their output; the Work directories are left as they are. It
// answers how many processes it could not end.
export async function endEveryWorkProcess(processes: ProcessTable, why: FinalDisconnection, log: (message: string) => void): Promise<number> {
  const release = await processes.holdStarts()
  try {
    let targets = await processes.running()
    let ended = 0
    for (let attempt = 1; ; attempt += 1) {
      const outcome = await processes.end(targets)
      ended += outcome.ended
      targets = outcome.remaining
      if (targets.length === 0 || attempt === ATTEMPTS) {
        if (targets.length > 0) {
          const reason = outcome.failure instanceof Error ? outcome.failure.message : String(outcome.failure)
          log(`could not end ${targets.length} Work processes (${WHY[why]}): ${reason}`)
        }
        log(`ended ${ended} Work processes: ${WHY[why]}`)
        return targets.length
      }
    }
  } finally {
    release()
  }
}
