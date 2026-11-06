import { execFile, execFileSync, spawn } from 'node:child_process'
import { constants, existsSync, openSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { Socket } from 'node:net'
import { setTimeout as delay } from 'node:timers/promises'
import { join } from 'node:path'
import type { CommandRun, ProcessHost } from './host.ts'
import { closeQuietly } from './output-log.ts'
import { stateFiles, type ProcessSpec, type StartedRecord } from './shim.ts'

// Linux and macOS: the command runs in bash as the leader of its own process
// group, reads stdin from a FIFO in its state directory, and is signalled as
// a group. A pid is told from a reused one by its command line.
// SIGKILL is delivered asynchronously; how long a kill waits for the shim.
const KILL_WAIT_MS = 5_000

export class PosixHost implements ProcessHost {
  private readonly useProcfs: boolean

  constructor(options: { useProcfs?: boolean } = {}) {
    this.useProcfs = options.useProcfs ?? procfs
  }

  startCommand(spec: ProcessSpec, env: NodeJS.ProcessEnv, stateDir: string): CommandRun {
    const fifo = join(stateDir, stateFiles.stdin)
    if (!existsSync(fifo)) execFileSync('mkfifo', ['-m', '600', fifo])
    // Read-write, so the command never reads end-of-file while nobody writes.
    const stdin = openSync(fifo, 'r+')
    const child = spawn('bash', ['-c', spec.command], {
      cwd: spec.cwd,
      // bash keeps the cwd as named, through a symlink such as macOS's
      // /var, only when PWD names it; otherwise it reports the resolved
      // path, which the Work root it was given does not match.
      env: { ...env, PWD: spec.cwd },
      detached: true,
      stdio: [stdin, 'pipe', 'pipe'],
    })
    child.once('spawn', () => closeQuietly(stdin))
    child.once('error', () => closeQuietly(stdin))
    return { child, ready: Promise.resolve(), close: () => {} }
  }

  // Opened without blocking: input for a process that no longer reads it is
  // dropped, as a closed pipe would.
  openStdin(stateDir: string): Socket | undefined {
    try {
      const fd = openSync(join(stateDir, stateFiles.stdin), constants.O_WRONLY | constants.O_NONBLOCK)
      return new Socket({ fd, readable: false, writable: true })
    } catch {
      return undefined
    }
  }

  shimAlive(started: StartedRecord, stateDir: string): Promise<boolean> {
    return shimAlive(started.shimPid, stateDir, this.useProcfs)
  }

  async signal(started: StartedRecord, signal: NodeJS.Signals): Promise<void> {
    if (started.pid > 0) killGroup(started.pid, signal)
  }

  // Returns once the shim is gone, as on Windows, so a run it ended is
  // recorded as killed by the next look rather than still running.
  async kill(started: StartedRecord, stateDir: string): Promise<void> {
    if (started.pid > 0) killGroup(started.pid, 'SIGKILL')
    try {
      process.kill(started.shimPid, 'SIGKILL')
    } catch {
      // Already gone.
    }
    const deadline = Date.now() + KILL_WAIT_MS
    while (Date.now() < deadline && (await this.shimAlive(started, stateDir))) await delay(20)
  }

  async reap(started: StartedRecord): Promise<void> {
    if (started.pid > 0 && (await isShell(started.pid, this.useProcfs))) killGroup(started.pid, 'SIGKILL')
  }

  shimEnv(): NodeJS.ProcessEnv {
    return {}
  }
}

function killGroup(pid: number, signal: NodeJS.Signals): void {
  try {
    process.kill(-pid, signal)
  } catch {
    // The group already ended.
  }
}

// Linux reads a process's command line from /proc; macOS has no /proc and
// answers through ps, unabridged (-ww) so a long command line keeps its end.
const procfs = existsSync('/proc/self/cmdline')

// The arguments pid runs with, or undefined once it is gone. Through ps they
// come as one line, joined by spaces.
export async function commandLine(pid: number, useProcfs = procfs): Promise<{ argv?: string[]; line?: string } | undefined> {
  if (useProcfs) {
    try {
      return { argv: (await readFile(`/proc/${pid}/cmdline`, 'utf8')).split('\0').filter((word) => word !== '') }
    } catch {
      return undefined
    }
  }
  const line = await ps(pid, 'command')
  return line === undefined ? undefined : { line }
}

// Whether pid is still this process's shim, not a later process that reused
// the pid: its arguments end with the state directory and the run.
export async function shimAlive(pid: number, dir: string, useProcfs = procfs): Promise<boolean> {
  const running = await commandLine(pid, useProcfs)
  if (running?.argv !== undefined) return running.argv.includes(dir)
  return running?.line !== undefined && new RegExp(` ${escapeRegExp(dir)} \\d+$`).test(running.line)
}

async function isShell(pid: number, useProcfs: boolean): Promise<boolean> {
  if (useProcfs) return (await commandLine(pid, true))?.argv?.[0]?.endsWith('bash') ?? false
  return (await ps(pid, 'comm'))?.endsWith('bash') ?? false
}

function ps(pid: number, field: 'command' | 'comm'): Promise<string | undefined> {
  return new Promise((resolve) => {
    execFile('ps', ['-ww', '-o', `${field}=`, '-p', String(pid)], (error, stdout) => {
      const line = stdout.trim()
      resolve(error !== null || line === '' ? undefined : line)
    })
  })
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
