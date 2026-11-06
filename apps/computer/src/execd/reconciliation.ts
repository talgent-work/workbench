import { randomUUID } from 'node:crypto'
import { isObject } from '../json.ts'
import type { CredentialStore } from '../proxy/credentials.ts'
import type { WorkInputs } from './inputs.ts'
import type { ProcessTable } from './processes.ts'
import type { ExecResponse } from './service.ts'
import { validId, type Works } from './works.ts'

// Reconciliation on reconnecting. While the machine was away the platform may
// have stopped or closed Works whose processes still run here, and seated other
// runs in the room they held. Each connection therefore opens a round: until
// the platform, which alone knows which Works still run here, has told the
// machine which processes to end and which Works' files to remove, no process
// is reserved or started. A process that already runs keeps running and may be
// attached to.

export class InvalidReconciliationError extends Error {}

interface Round {
  id: string
  applied: boolean
  done: Promise<void>
  open: () => void
}

export interface ReconciliationOptions {
  works: Works
  processes: ProcessTable
  credentials: CredentialStore
  inputs: WorkInputs
  log: (message: string) => void
}

// What the platform decided, applied as it was given.
interface Decision {
  round: string
  removeWorks: string[]
  endProcesses: { workId: string; processId: string }[]
}

export class Reconciliation {
  private readonly options: ReconciliationOptions
  // Before any connection nothing can start, so the first round begins open.
  private round: Round = openRound()
  // Applications run one at a time.
  private applying: Promise<unknown> = Promise.resolve()

  constructor(options: ReconciliationOptions) {
    this.options = options
  }

  // A connection was welcomed: a new round begins. On a platform that
  // reconciles, starts that waited for an earlier round not applied yet keep
  // waiting, now for this one. A platform that does not reconcile never
  // decides a round, so its round begins decided and releases every start
  // that waited.
  begin(reconciles: boolean): void {
    const previous = this.round
    if (!reconciles) {
      this.round = openRound()
      previous.open()
      return
    }
    this.round = previous.applied ? pendingRound() : { ...previous, id: randomUUID() }
  }

  // Whether starts wait for the platform's decision.
  pending(): boolean {
    return !this.round.applied
  }

  // Resolves once the current round is applied, or when signal aborts.
  settled(signal: AbortSignal): Promise<void> {
    if (this.round.applied || signal.aborted) return Promise.resolve()
    const { done } = this.round
    return new Promise((resolve) => {
      const stop = () => resolve()
      signal.addEventListener('abort', stop, { once: true })
      void done.then(() => {
        signal.removeEventListener('abort', stop)
        resolve()
      })
    })
  }

  // GET: the round, and while it is pending every Work this machine holds
  // anything of, with its processes and those of them that exited, which an
  // attach would run again.
  async inventory(): Promise<ExecResponse> {
    const { id, applied } = this.round
    if (applied) return { status: 200, body: { round: id, reconciled: true, works: [] } }
    const table = this.options.processes
    const processes = await table.known()
    for (const workId of await this.options.works.list()) if (!processes.has(workId)) processes.set(workId, [])
    const works = []
    for (const [workId, all] of processes) {
      if (!validId(workId)) continue
      const ids = all.filter(validId)
      const exited = []
      for (const processId of ids) if ((await table.phase(workId, processId))?.kind === 'exited') exited.push(processId)
      works.push({ work_id: workId, processes: ids, exited })
    }
    return { status: 200, body: { round: id, reconciled: false, works } }
  }

  // POST: ends the named processes and removes the named Works, then opens
  // the round it was decided for. A decision for an earlier round is
  // refused: what ran since the machine reconnected again is not in it. One
  // for the round already decided changes nothing.
  async apply(body: unknown): Promise<ExecResponse> {
    let decision: Decision
    try {
      decision = parseDecision(body)
    } catch (error) {
      if (error instanceof InvalidReconciliationError) return { status: 400, body: { code: 'INVALID_REQUEST_BODY', message: error.message } }
      throw error
    }
    const applying = this.applying.catch(() => {}).then(() => this.applyNow(decision))
    this.applying = applying
    return applying
  }

  private async applyNow(decision: Decision): Promise<ExecResponse> {
    if (decision.round !== this.round.id) return superseded
    // A repeated or late decision for a round already decided changes
    // nothing: what started since must not be ended by it.
    if (this.round.applied) return { status: 204 }
    const { processes, works, credentials, inputs, log } = this.options
    let ended = 0
    for (const { workId, processId } of decision.endProcesses) {
      if (await processes.delete(workId, processId)) ended += 1
    }
    for (const workId of decision.removeWorks) {
      await processes.deleteWork(workId)
      await credentials.delete(workId)
      await inputs.remove(workId)
      await works.remove(workId)
    }
    // The connection may have changed while the decision was applied.
    if (decision.round !== this.round.id) return superseded
    if (!this.round.applied) {
      this.round.applied = true
      this.round.open()
      log(`reconciled: ended ${ended} Work processes and removed ${decision.removeWorks.length} Works gone from this machine`)
    }
    return { status: 204 }
  }
}

const superseded: ExecResponse = { status: 409, body: { code: 'ROUND_SUPERSEDED', message: 'the machine reconnected since this round began' } }

function openRound(): Round {
  return { id: randomUUID(), applied: true, done: Promise.resolve(), open: () => {} }
}

function pendingRound(): Round {
  let open!: () => void
  const done = new Promise<void>((resolve) => { open = resolve })
  return { id: randomUUID(), applied: false, done, open }
}

function parseDecision(body: unknown): Decision {
  if (!isObject(body)) throw new InvalidReconciliationError('body must be an object')
  const { round, remove_works: removeWorks = [], end_processes: endProcesses = [] } = body
  if (typeof round !== 'string' || round === '') throw new InvalidReconciliationError('round required')
  if (!Array.isArray(removeWorks) || !removeWorks.every((id) => typeof id === 'string' && validId(id))) {
    throw new InvalidReconciliationError('remove_works must list Work ids')
  }
  if (!Array.isArray(endProcesses)) throw new InvalidReconciliationError('end_processes must be a list')
  return {
    round,
    removeWorks,
    endProcesses: endProcesses.map((entry: unknown) => {
      if (!isObject(entry) || typeof entry.work_id !== 'string' || typeof entry.process_id !== 'string' || !validId(entry.work_id) || !validId(entry.process_id)) {
        throw new InvalidReconciliationError('end_processes must name a work_id and a process_id')
      }
      return { workId: entry.work_id, processId: entry.process_id }
    }),
  }
}
