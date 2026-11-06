import { mkdir, readFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import type { EndOutcome, RunningProcess } from './execd/processes.ts'
import { writeJsonAtomic } from './execd/shim.ts'
import { isErrno } from './store.ts'

// Fencing: Agent stops a Work for Disconnection 5 minutes after the Computer
// Gateway saw the connection end, so Talgent Computer ends every Work process
// it runs before then. Both values change together.
export const FENCE_AFTER_MS = 270_000
// How often the silence is measured. Timers pause while the machine sleeps,
// so the fence never waits out one long timer: each check reads the wall
// clock, and the first check after waking sees the whole sleep.
const CHECK_EVERY_MS = 5_000
// How often the last contact is written to disk. A record up to this much
// behind only fences earlier.
const PERSIST_EVERY_MS = 10_000

export interface FenceOptions {
  // Where the last contact is kept, so a restart while offline still fences
  // on time: Work processes outlive Talgent Computer through their shims.
  stateFile: string
  // The Work processes the fence ends (ProcessTable).
  processes: FencedProcesses
  log: (message: string) => void
  now?: () => number
  // Runs check periodically until the returned function cancels it.
  every?: (check: () => void) => () => void
}

export interface FencedProcesses {
  holdStarts: () => Promise<() => void>
  running: () => Promise<RunningProcess[]>
  end: (targets: RunningProcess[]) => Promise<EndOutcome>
}

interface FenceRecord {
  last_heard_at: number
}

export class Fence {
  private readonly options: FenceOptions
  private readonly now: () => number
  private lastHeardAt: number
  private persistedAt = Number.NEGATIVE_INFINITY
  // This silence has reached the threshold; it does not fence again until
  // contact resumes.
  private silenceFenced = false
  // A fence fired and has not listed its processes yet: starts stay held
  // until it has, so the list is what ran when it fired.
  private holding: Promise<() => void> | undefined
  // The processes a fence listed and has not ended yet. Retries end only
  // these, so contact resuming meanwhile never widens what is ended.
  private targets: RunningProcess[] | undefined
  private ended = 0
  private silentMs = 0
  private ending: Promise<void> | undefined
  private cancel: (() => void) | undefined

  constructor(options: FenceOptions) {
    this.options = options
    this.now = options.now ?? Date.now
    this.lastHeardAt = this.now()
  }

  // Starts from the recorded last contact when it lies in the past, and
  // checks at once, so a restart long after contact fences before
  // connecting.
  async start(): Promise<void> {
    await mkdir(dirname(this.options.stateFile), { recursive: true, mode: 0o700 })
    const recorded = await this.recorded()
    const now = this.now()
    this.lastHeardAt = recorded !== undefined && recorded < now ? recorded : now
    this.persistedAt = recorded ?? Number.NEGATIVE_INFINITY
    const every = this.options.every ?? everyInterval
    this.cancel = every(() => void this.check())
    await this.check()
  }

  async stop(): Promise<void> {
    this.cancel?.()
    this.cancel = undefined
    await this.ending
    const holding = this.holding
    this.holding = undefined
    if (holding !== undefined) (await holding)()
    if (this.persistedAt !== this.lastHeardAt) this.persist()
  }

  // Contact from the service. The silence it ends is measured first, so a
  // frame arriving right after a long sleep, or on a reconnection, still
  // fences what the platform already stopped. Resolves once any fencing it
  // fired has been tried; never rejects.
  heard(): Promise<void> {
    const ended = this.check()
    this.lastHeardAt = this.now()
    this.silenceFenced = false
    if (this.lastHeardAt - this.persistedAt >= PERSIST_EVERY_MS) this.persist()
    return ended
  }

  // Fences once the silence reached the threshold, and retries a fence
  // whose processes are not all listed or ended yet. Starts are held before
  // anything is awaited, so contact that fires the fence cannot start a
  // process it then ends.
  check(): Promise<void> {
    const silentMs = this.now() - this.lastHeardAt
    if (!this.silenceFenced && silentMs >= FENCE_AFTER_MS) {
      this.silenceFenced = true
      this.silentMs = silentMs
      this.holding ??= this.options.processes.holdStarts()
    }
    if ((this.holding !== undefined || this.targets !== undefined) && this.ending === undefined) {
      this.ending = this.end().finally(() => { this.ending = undefined })
    }
    return this.ending ?? Promise.resolve()
  }

  private async end(): Promise<void> {
    try {
      if (this.holding !== undefined) {
        const release = await this.holding
        // A process still running from an earlier fence is listed again.
        this.targets = await this.options.processes.running()
        this.holding = undefined
        release()
      }
      const outcome = await this.options.processes.end(this.targets ?? [])
      this.ended += outcome.ended
      if (outcome.remaining.length > 0) {
        this.targets = outcome.remaining
        throw outcome.failure ?? new Error(`could not end ${outcome.remaining.length} Work processes`)
      }
      this.targets = undefined
      this.options.log(`fenced: ended ${this.ended} Work processes after ${Math.round(this.silentMs / 1000)}s without the service`)
      this.ended = 0
    } catch (error) {
      this.options.log(`fencing failed, retrying on the next check: ${error instanceof Error ? error.message : String(error)}`)
    }
  }

  private async recorded(): Promise<number | undefined> {
    let text: string
    try {
      text = await readFile(this.options.stateFile, 'utf8')
    } catch (error) {
      if (isErrno(error, 'ENOENT')) return undefined
      throw error
    }
    try {
      const value = (JSON.parse(text) as Partial<FenceRecord>).last_heard_at
      if (typeof value === 'number' && Number.isFinite(value)) return value
    } catch {
      // Unreadable, as a record cut short; counted from now below.
    }
    this.options.log(`ignoring the unreadable fence record ${this.options.stateFile}`)
    return undefined
  }

  private persist(): void {
    const at = this.lastHeardAt
    try {
      writeJsonAtomic(this.options.stateFile, { last_heard_at: at } satisfies FenceRecord)
      this.persistedAt = at
    } catch (error) {
      // The in-memory clock still fences this run; the next contact retries.
      this.options.log(`cannot record the last contact: ${error instanceof Error ? error.message : String(error)}`)
    }
  }
}

function everyInterval(check: () => void): () => void {
  const timer = setInterval(check, CHECK_EVERY_MS)
  timer.unref()
  return () => clearInterval(timer)
}
