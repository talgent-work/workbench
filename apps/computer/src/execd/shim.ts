import { readFileSync, renameSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { CommandRun, ProcessHost } from './host.ts'
import { LogAppender, STDERR, STDOUT } from './output-log.ts'

// The shim is one run of one Work process, started detached from Talgent
// Computer so the process outlives Talgent Computer's restarts and upgrades.
// It owns the run's output: it appends every output byte to the process's
// log, and writes the run's exit code once the command ended. Talgent
// Computer reads both from the process's state directory, and writes stdin
// and delivers signals through the machine's ProcessHost.

export const SHIM_ARG = '__shim'
// The command's environment, handed over as JSON so variables meant for the
// command (NODE_OPTIONS, say) never configure the shim's own node.
export const SHIM_ENV = 'TALGENT_SHIM_ENV'
// Bytes the command may still write after its shell exited, while
// descendants hold its stdout; execd does not wait for them either.
const DRAIN_AFTER_EXIT_MS = 100

export const stateFiles = {
  spec: 'spec.json',
  log: 'output.log',
  // The stdin FIFO, on Linux and macOS.
  stdin: 'stdin',
  started: 'started.json',
  exit: 'exit.json',
  // The hash of the current run's local proxy token (run-tokens.ts).
  proxyToken: 'proxy-token.json',
  // What the current run uses that outlives Talgent Computer's run, such as
  // its Claude Code copy (processes.ts).
  uses: 'uses.json',
} as const

export interface ProcessSpec {
  command: string
  cwd: string
}

export interface StartedRecord {
  run: number
  shimPid: number
  // The command's pid, which on Linux and macOS also names its process
  // group; 0 when the command could not be started.
  pid: number
}

export interface UsesRecord {
  run: number
  uses: string[]
}

export interface ExitRecord {
  run: number
  exitCode: number
}

export function writeJsonAtomic(path: string, value: unknown): void {
  const tmp = `${path}.${process.pid}.tmp`
  writeFileSync(tmp, JSON.stringify(value), { mode: 0o600 })
  renameAtomic(tmp, path)
}

// Windows refuses to replace a file another process has open for a moment,
// as a reader of the record may; the rename is retried briefly.
function renameAtomic(from: string, to: string): void {
  for (let attempt = 0; ; attempt += 1) {
    try {
      renameSync(from, to)
      return
    } catch (error) {
      const code = (error as NodeJS.ErrnoException).code
      if (process.platform !== 'win32' || attempt >= 20 || (code !== 'EPERM' && code !== 'EACCES' && code !== 'EBUSY')) throw error
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 25)
    }
  }
}

export function runShim(stateDir: string, run: number, host: ProcessHost): void {
  const spec = JSON.parse(readFileSync(join(stateDir, stateFiles.spec), 'utf8')) as ProcessSpec
  const env = JSON.parse(process.env[SHIM_ENV] ?? '{}') as NodeJS.ProcessEnv
  const log = new LogAppender(join(stateDir, stateFiles.log))
  const append = (stream: number, chunk: Buffer) => log.append(stream, chunk)
  let recorded = false
  const started = (pid: number) => {
    if (recorded) return
    recorded = true
    writeJsonAtomic(join(stateDir, stateFiles.started), { run, shimPid: process.pid, pid } satisfies StartedRecord)
  }
  let command: CommandRun | undefined
  const finish = (exitCode: number) => {
    // A command may end before its stdin channel was up.
    started(command?.child.pid ?? 0)
    command?.close()
    log.close()
    try {
      writeJsonAtomic(join(stateDir, stateFiles.exit), { run, exitCode } satisfies ExitRecord)
    } catch {
      // The process was deleted while it ran.
    }
    process.exit(0)
  }
  const failed = (message: string) => {
    append(STDERR, Buffer.from(`talgent-computer: cannot start the command: ${message}\n`))
    started(0)
    finish(-1)
  }

  try {
    command = host.startCommand(spec, env, stateDir, run)
  } catch (error) {
    return failed(error instanceof Error ? error.message : String(error))
  }
  const { child } = command
  const { stdout, stderr } = child
  if (stdout === null || stderr === null) throw new Error('the command has no output pipes')
  stdout.on('data', (chunk: Buffer) => append(STDOUT, chunk))
  stderr.on('data', (chunk: Buffer) => append(STDERR, chunk))
  const spawned = new Promise<void>((resolve) => child.once('spawn', resolve))
  // Writers reach the command once both its process and its stdin are up.
  Promise.all([spawned, command.ready]).then(
    () => started(child.pid ?? 0),
    (error: unknown) => {
      child.kill('SIGKILL')
      failed(error instanceof Error ? error.message : String(error))
    },
  )
  child.once('error', (error) => failed(error.message))
  child.once('exit', (code) => {
    // A shell killed by a signal has no exit code; execd reports -1.
    const exitCode = code ?? -1
    let open = 2
    const done = () => {
      open -= 1
      if (open === 0) finish(exitCode)
    }
    for (const stream of [stdout, stderr]) {
      if (stream.closed) done()
      else stream.once('close', done)
    }
    setTimeout(() => finish(exitCode), DRAIN_AFTER_EXIT_MS)
  })
}
