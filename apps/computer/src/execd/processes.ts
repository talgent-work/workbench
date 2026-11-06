import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { closeSync, openSync } from 'node:fs'
import { mkdir, open, readdir, readFile, rm, stat } from 'node:fs/promises'
import type { Socket } from 'node:net'
import { join } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { Limit } from '../limit.ts'
import { isErrno } from '../store.ts'
import { currentHost, type ProcessHost } from './host.ts'
import { LogCursor } from './output-log.ts'
import { RunTokens } from './run-tokens.ts'
import { SHIM_ENV, stateFiles, writeJsonAtomic, type ExitRecord, type ProcessSpec, type StartedRecord, type UsesRecord } from './shim.ts'

// How a Work process stands, read from its state directory so it holds across
// Talgent Computer restarts. A reservation has never started; execd starts
// a reservation, or runs an exited process again, on attach.
export type ProcessPhase =
  | { kind: 'reserved' }
  | { kind: 'running'; started: StartedRecord }
  | { kind: 'exited'; run: number; exitCode: number }

export const SIGNALS = new Set(['SIGINT', 'SIGTERM', 'SIGKILL'])
const START_TIMEOUT_MS = 15_000
// Processes running and end look at and end at a time.
const FENCE_CONCURRENCY = 8

// One run of a Work process, as fencing finds and ends it.
export interface RunningProcess {
  workId: string
  id: string
  run: number
}

// What end did: how many runs it ended, the targets it could not end, and
// the first reason why.
export interface EndOutcome {
  ended: number
  remaining: RunningProcess[]
  failure?: unknown
}

// The environment of a run, given the run's local proxy token.
export type RunEnvironment = (proxyToken: string) => Promise<NodeJS.ProcessEnv>

// What Talgent Computer keeps in memory per known process: who holds its
// pipe, the log checkpoints read so far, and listeners for its deletion.
interface Entry {
  held: boolean
  checkpoints: { position: number; offset: number }[]
  deleted: AbortController
  starting?: Promise<unknown>
}

// The processes of all Works on this machine, one state directory each under
// <state root>/<work id>/<process id>/.
export class ProcessTable {
  private readonly stateRoot: string
  // The command and leading arguments that run the shim; the state directory
  // and run number follow.
  private readonly shim: string[]
  private readonly entries = new Map<string, Entry>()
  private readonly tokens: RunTokens
  private readonly host: ProcessHost
  // Closed while any hold is taken, as fencing takes its snapshot: a start
  // begun meanwhile waits, so the snapshot never races a process launched
  // after the fence fired. Holds nest: the gate opens once every holder
  // released it.
  private held: Promise<void> | undefined
  private openHeld: (() => void) | undefined
  private holds = 0
  private readonly launching = new Set<Promise<unknown>>()
  // What every run this table starts uses, recorded with the run.
  private readonly uses: string[]

  constructor(options: { stateRoot: string; shim: string[]; uses?: string[]; host?: ProcessHost }) {
    this.stateRoot = options.stateRoot
    this.shim = options.shim
    this.uses = options.uses ?? []
    this.host = options.host ?? currentHost()
    this.tokens = new RunTokens({ dirOf: (workId, id) => this.dir(workId, id), workDir: (workId) => join(this.stateRoot, workId) })
  }

  dir(workId: string, id: string): string {
    return join(this.stateRoot, workId, id)
  }

  async reserve(workId: string, spec: ProcessSpec): Promise<string> {
    const id = randomUUID()
    const dir = this.dir(workId, id)
    await mkdir(dir, { recursive: true, mode: 0o700 })
    writeJsonAtomic(join(dir, stateFiles.spec), spec)
    return id
  }

  async exists(workId: string, id: string): Promise<boolean> {
    try {
      await stat(join(this.dir(workId, id), stateFiles.spec))
      return true
    } catch (error) {
      if (isErrno(error, 'ENOENT') || isErrno(error, 'ENOTDIR')) return false
      throw error
    }
  }

  entry(workId: string, id: string): Entry {
    const key = `${workId}/${id}`
    let entry = this.entries.get(key)
    if (entry === undefined) {
      entry = { held: false, checkpoints: [], deleted: new AbortController() }
      this.entries.set(key, entry)
    }
    return entry
  }

  // One pipe holds a process at a time.
  acquire(workId: string, id: string): boolean {
    const entry = this.entry(workId, id)
    if (entry.held) return false
    entry.held = true
    return true
  }

  release(workId: string, id: string): void {
    this.entry(workId, id).held = false
  }

  async phase(workId: string, id: string): Promise<ProcessPhase | undefined> {
    if (!(await this.exists(workId, id))) return undefined
    const starting = this.entries.get(`${workId}/${id}`)?.starting
    if (starting !== undefined) await starting.catch(() => {})
    return this.observe(workId, id)
  }

  // The phase as the state directory records it, without waiting for a
  // start: one held at the gate has written nothing yet.
  private async observe(workId: string, id: string): Promise<ProcessPhase | undefined> {
    if (!(await this.exists(workId, id))) return undefined
    const dir = this.dir(workId, id)
    const started = await readJson<StartedRecord>(join(dir, stateFiles.started))
    if (started === undefined) return { kind: 'reserved' }
    const exited = await this.exitOf(dir, started.run)
    if (exited !== undefined) return exited
    if (await this.host.shimAlive(started, dir)) return { kind: 'running', started }
    // The shim finished between the reads above, or died without recording
    // the exit: end what it left and record the run as killed.
    const settled = await this.exitOf(dir, started.run)
    if (settled !== undefined) return settled
    await this.host.reap(started)
    writeJsonAtomic(join(dir, stateFiles.exit), { run: started.run, exitCode: -1 } satisfies ExitRecord)
    return { kind: 'exited', run: started.run, exitCode: -1 }
  }

  private async exitOf(dir: string, run: number): Promise<ProcessPhase | undefined> {
    const exit = await readJson<ExitRecord>(join(dir, stateFiles.exit))
    return exit !== undefined && exit.run === run ? { kind: 'exited', run, exitCode: exit.exitCode } : undefined
  }

  // Whether token is the local proxy token of a running run of one of
  // workId's processes; a run's token ends with the run.
  async proxyTokenValid(workId: string, token: string): Promise<boolean> {
    const holder = await this.tokens.find(workId, token)
    if (holder === undefined) return false
    const phase = await this.phase(workId, holder.id)
    return phase?.kind === 'running' && phase.started.run === holder.run
  }

  // Starts a new run of a reserved or exited process, with a fresh local
  // proxy token and the environment env composes for it, waits until the
  // shim reports it started, and answers the run.
  start(workId: string, id: string, env: RunEnvironment): Promise<number> {
    const entry = this.entry(workId, id)
    const starting = this.pastGate(() => this.launch(workId, id, env)).finally(() => {
      if (entry.starting === starting) entry.starting = undefined
    })
    entry.starting = starting
    return starting
  }

  // Launches once no hold is closed, checking the gate and registering the
  // launch in one step, so a hold never misses a launch that passed it.
  private pastGate(launch: () => Promise<number>): Promise<number> {
    if (this.held !== undefined) return this.held.then(() => this.pastGate(launch))
    const launched = launch()
    this.launching.add(launched)
    const settled = () => { this.launching.delete(launched) }
    launched.then(settled, settled)
    return launched
  }

  // Holds every start begun from now on until the returned release is
  // called, and resolves once the launches already past the gate settled,
  // so what running then lists is every run that began before the hold.
  // The gate closes before the first await, as fencing needs. Holders, as
  // the fence and the end of every Work process, may hold at once: starts
  // wait until each released its hold, and releasing twice counts once.
  holdStarts(): Promise<() => void> {
    if (this.holds === 0) this.held = new Promise<void>((resolve) => { this.openHeld = resolve })
    this.holds += 1
    let released = false
    const release = () => {
      if (released) return
      released = true
      this.holds -= 1
      if (this.holds > 0) return
      const open = this.openHeld
      this.held = undefined
      this.openHeld = undefined
      open?.()
    }
    return Promise.allSettled([...this.launching]).then(() => release)
  }

  // The running runs of every Work's processes. It reads each without
  // waiting for a start, so it can be taken while starts are held.
  async running(): Promise<RunningProcess[]> {
    const found: RunningProcess[] = []
    const limit = new Limit(FENCE_CONCURRENCY)
    const processes: [string, string][] = []
    for (const workId of await subdirectories(this.stateRoot)) {
      for (const id of await subdirectories(join(this.stateRoot, workId))) processes.push([workId, id])
    }
    await Promise.all(processes.map(([workId, id]) => limit.run(async () => {
      const phase = await this.observe(workId, id)
      if (phase?.kind === 'running') found.push({ workId, id, run: phase.started.run })
    })))
    return found
  }

  // The Works that have process state on this machine, and each one's
  // processes, as reconciliation reports them.
  async known(): Promise<Map<string, string[]>> {
    const found = new Map<string, string[]>()
    for (const workId of await subdirectories(this.stateRoot)) found.set(workId, await subdirectories(join(this.stateRoot, workId)))
    return found
  }

  // Kills and forgets every process of the Work, then its state directory.
  async deleteWork(workId: string): Promise<void> {
    for (const id of await subdirectories(join(this.stateRoot, workId))) await this.delete(workId, id)
    await rm(join(this.stateRoot, workId), { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
  }

  // What the running runs use, as each recorded it when it started; a run
  // started by an earlier Talgent Computer uses what that one gave it.
  async inUse(): Promise<Set<string>> {
    const used = new Set<string>()
    for (const { workId, id, run } of await this.running()) {
      const record = await readJson<UsesRecord>(join(this.dir(workId, id), stateFiles.uses))
      if (record?.run === run && Array.isArray(record.uses)) for (const use of record.uses) used.add(use)
    }
    return used
  }

  // Ends each target while it still runs the same run, keeping it known with
  // its output: its next look records the run killed (-1). A later run of
  // the same process is not the target's and is left. Every target is
  // tried; those that could not be ended are answered for a retry.
  async end(targets: RunningProcess[]): Promise<EndOutcome> {
    const limit = new Limit(FENCE_CONCURRENCY)
    const results = await Promise.allSettled(targets.map((target) => limit.run(async () => {
      const phase = await this.phase(target.workId, target.id)
      if (phase?.kind !== 'running' || phase.started.run !== target.run) return false
      await this.host.kill(phase.started, this.dir(target.workId, target.id))
      return true
    })))
    const outcome: EndOutcome = { ended: 0, remaining: [] }
    results.forEach((result, index) => {
      if (result.status === 'fulfilled') {
        if (result.value) outcome.ended += 1
        return
      }
      outcome.remaining.push(targets[index])
      outcome.failure ??= result.reason
    })
    return outcome
  }

  private async launch(workId: string, id: string, runEnv: RunEnvironment): Promise<number> {
    const dir = this.dir(workId, id)
    const previous = await readJson<StartedRecord>(join(dir, stateFiles.started))
    const run = (previous?.run ?? 0) + 1
    const env = await runEnv(await this.tokens.mint(workId, id, run))
    // Before the run starts, so a run never runs unrecorded.
    writeJsonAtomic(join(dir, stateFiles.uses), { run, uses: this.uses } satisfies UsesRecord)
    const output = openSync(join(dir, 'shim.log'), 'a', 0o600)
    let shimExited = false
    try {
      const [command, ...args] = this.shim
      const child = spawn(command, [...args, dir, String(run)], {
        cwd: dir,
        detached: true,
        windowsHide: true,
        stdio: ['ignore', output, output],
        env: { PATH: process.env.PATH ?? '', ...this.host.shimEnv(), [SHIM_ENV]: JSON.stringify(env) },
      })
      child.once('exit', () => { shimExited = true })
      child.once('error', () => { shimExited = true })
      child.unref()
    } finally {
      closeSync(output)
    }
    const deadline = Date.now() + START_TIMEOUT_MS
    for (;;) {
      const started = await readJson<StartedRecord>(join(dir, stateFiles.started))
      if (started?.run === run) return run
      if (shimExited) {
        const again = await readJson<StartedRecord>(join(dir, stateFiles.started))
        if (again?.run === run) return run
        throw new Error('the process shim exited before starting the command')
      }
      if (Date.now() > deadline) throw new Error('the process shim did not start the command in time')
      await delay(10)
    }
  }

  async signal(workId: string, id: string, signal: string): Promise<void> {
    if (!SIGNALS.has(signal)) return
    const phase = await this.phase(workId, id)
    if (phase?.kind === 'running') await this.host.signal(phase.started, signal as NodeJS.Signals, this.dir(workId, id))
  }

  // A writer to the stdin of the process's run.
  openStdin(workId: string, id: string, run: number): Socket | undefined {
    return this.host.openStdin(this.dir(workId, id), run)
  }

  // Kills the process group and forgets the process. False when unknown.
  async delete(workId: string, id: string): Promise<boolean> {
    const phase = await this.phase(workId, id)
    if (phase === undefined) return false
    if (phase.kind === 'running') await this.host.kill(phase.started, this.dir(workId, id))
    const entry = this.entry(workId, id)
    entry.deleted.abort()
    this.entries.delete(`${workId}/${id}`)
    // Windows keeps a directory a just-killed shim held files in busy for a moment.
    await rm(this.dir(workId, id), { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
    await this.tokens.forget(workId, id)
    return true
  }

  // Output bytes the process has written so far.
  async outputOffset(workId: string, id: string): Promise<number> {
    const cursor = await this.cursor(workId, id)
    try {
      cursor.seek(Number.MAX_SAFE_INTEGER)
      while ((await cursor.next()).length > 0) { /* read to the end */ }
      return cursor.offset
    } finally {
      await cursor.close()
    }
  }

  async cursor(workId: string, id: string): Promise<LogCursor> {
    const handle = await open(join(this.dir(workId, id), stateFiles.log), 'a+', 0o600)
    return new LogCursor(handle, this.entry(workId, id).checkpoints)
  }
}

async function readJson<T>(path: string): Promise<T | undefined> {
  try {
    return JSON.parse(await readFile(path, 'utf8')) as T
  } catch (error) {
    if (isErrno(error, 'ENOENT') || isErrno(error, 'ENOTDIR')) return undefined
    throw error
  }
}

async function subdirectories(dir: string): Promise<string[]> {
  try {
    return (await readdir(dir, { withFileTypes: true })).filter((entry) => entry.isDirectory()).map((entry) => entry.name)
  } catch (error) {
    if (isErrno(error, 'ENOENT') || isErrno(error, 'ENOTDIR')) return []
    throw error
  }
}
