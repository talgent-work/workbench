import { execFile, spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync } from 'node:fs'
import { connect, createServer, type Socket } from 'node:net'
import { win32 } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import type { CommandRun, ProcessHost } from './host.ts'
import type { ProcessSpec, StartedRecord } from './shim.ts'

// Windows has no FIFOs, process groups or POSIX signals. The command runs in
// Git Bash, which Claude Code needs on Windows as well, so the platform's
// commands run as they do in a Cloud sandbox. Its stdin arrives through a
// named pipe the shim serves for the run; the pipe exists exactly while the
// shim does, which also tells the shim from a later process with its pid.
// Every signal ends the run's process tree, as Windows offers no way to
// interrupt a console process of another console.

export interface WindowsHostOptions {
  // The run's pipe; a named pipe on Windows.
  pipePath?: (stateDir: string, run: number) => string
  // Ends pid and its descendants.
  killTree?: (pid: number) => Promise<void>
  // The bash the command runs in, given its environment.
  shell?: (env: NodeJS.ProcessEnv) => string
}

// How long kill waits for a killed shim's pipe to close.
const KILL_WAIT_MS = 5_000

export class WindowsHost implements ProcessHost {
  private readonly pipePath: (stateDir: string, run: number) => string
  private readonly killTree: (pid: number) => Promise<void>
  private readonly shell: (env: NodeJS.ProcessEnv) => string

  constructor(options: WindowsHostOptions = {}) {
    this.pipePath = options.pipePath ?? namedPipe
    this.killTree = options.killTree ?? taskkill
    this.shell = options.shell ?? requireGitBash
  }

  startCommand(spec: ProcessSpec, env: NodeJS.ProcessEnv, stateDir: string, run: number): CommandRun {
    const child = spawn(this.shell(env), ['-c', spec.command], {
      cwd: spec.cwd,
      env,
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
    })
    const stdin = child.stdin
    if (stdin === null) throw new Error('the command has no stdin pipe')
    stdin.on('error', () => {})
    // Several writers may come and go; none ends the command's stdin.
    const server = createServer((socket) => {
      socket.on('error', () => {})
      socket.pipe(stdin, { end: false })
    })
    const ready = new Promise<void>((resolve, reject) => {
      server.once('listening', resolve)
      server.once('error', reject)
    })
    server.listen(this.pipePath(stateDir, run))
    return { child, ready, close: () => server.close() }
  }

  openStdin(stateDir: string, run: number): Socket | undefined {
    return connect(this.pipePath(stateDir, run))
  }

  shimAlive(started: StartedRecord, stateDir: string): Promise<boolean> {
    return pipeServed(this.pipePath(stateDir, started.run))
  }

  async signal(started: StartedRecord, _signal: NodeJS.Signals, stateDir: string): Promise<void> {
    await this.kill(started, stateDir)
  }

  // Ends the shim with the command beneath it. taskkill /T may end the
  // command first, and the shim then records its exit code; ProcessTable.end
  // records the ended run as killed either way.
  async kill(started: StartedRecord, stateDir: string): Promise<void> {
    await this.killTree(started.shimPid)
    const deadline = Date.now() + KILL_WAIT_MS
    while (Date.now() < deadline && (await this.shimAlive(started, stateDir))) await delay(50)
  }

  // A dead shim takes the shell with it, which Windows ends as a member of
  // the shim's job. Its descendants have no tree left to find without
  // risking a reused pid, so nothing more is ended.
  async reap(): Promise<void> {}

  shimEnv(): NodeJS.ProcessEnv {
    const env: NodeJS.ProcessEnv = {}
    for (const name of ['SystemRoot', 'windir', 'SystemDrive', 'TEMP', 'TMP', 'ProgramFiles', 'ProgramW6432', 'LOCALAPPDATA', 'CLAUDE_CODE_GIT_BASH_PATH']) {
      const value = variable(process.env, name)
      if (value !== undefined) env[name] = value
    }
    return env
  }
}

export function namedPipe(stateDir: string, run: number): string {
  const digest = createHash('sha256').update(`${stateDir}\0${run}`).digest('hex').slice(0, 40)
  return `\\\\.\\pipe\\talgent-computer-${digest}`
}

// Whether a server answers on path: a pipe nobody serves is gone, or on
// POSIX sockets refuses. Anything else, a busy pipe say, is a live shim.
export function pipeServed(path: string): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = connect(path)
    socket.once('connect', () => {
      socket.destroy()
      resolve(true)
    })
    socket.once('error', (error: NodeJS.ErrnoException) => {
      socket.destroy()
      resolve(error.code !== 'ENOENT' && error.code !== 'ECONNREFUSED')
    })
  })
}

function taskkill(pid: number): Promise<void> {
  return new Promise((resolve) => {
    execFile('taskkill', ['/PID', String(pid), '/T', '/F'], { windowsHide: true }, () => resolve())
  })
}

export class GitBashMissingError extends Error {}

// The command's environment decides first; the shim's own, which carries
// Talgent Computer's, follows.
function requireGitBash(env: NodeJS.ProcessEnv): string {
  const bash = findGitBash(env) ?? findGitBash(process.env)
  if (bash === undefined) {
    throw new GitBashMissingError('Git for Windows is required to run Work commands; install it from https://git-scm.com/downloads/win or set CLAUDE_CODE_GIT_BASH_PATH to its bash.exe')
  }
  return bash
}

// Git Bash, found as Claude Code finds it: CLAUDE_CODE_GIT_BASH_PATH, then
// beside a git.exe on PATH, then the default install locations. The bash.exe
// in System32 is WSL's, not Git's, and never chosen.
export function findGitBash(env: NodeJS.ProcessEnv, exists: (path: string) => boolean = existsSync): string | undefined {
  const configured = variable(env, 'CLAUDE_CODE_GIT_BASH_PATH')
  if (configured !== undefined && exists(configured)) return configured
  const candidates: string[] = []
  for (const dir of (variable(env, 'PATH') ?? '').split(';')) {
    if (dir === '' || !exists(win32.join(dir, 'git.exe'))) continue
    // <Git>\cmd\git.exe or <Git>\bin\git.exe; bash lives in <Git>\bin.
    candidates.push(win32.join(dir, '..', 'bin', 'bash.exe'), win32.join(dir, 'bash.exe'))
  }
  for (const base of ['ProgramFiles', 'ProgramW6432', 'ProgramFiles(x86)']) {
    const dir = variable(env, base)
    if (dir !== undefined) candidates.push(win32.join(dir, 'Git', 'bin', 'bash.exe'))
  }
  const local = variable(env, 'LOCALAPPDATA')
  if (local !== undefined) candidates.push(win32.join(local, 'Programs', 'Git', 'bin', 'bash.exe'))
  return candidates.find((candidate) => exists(candidate))
}

// Windows names variables case-insensitively.
function variable(env: NodeJS.ProcessEnv, name: string): string | undefined {
  const upper = name.toUpperCase()
  for (const [key, value] of Object.entries(env)) {
    if (key.toUpperCase() === upper && value !== undefined && value !== '') return value
  }
  return undefined
}
