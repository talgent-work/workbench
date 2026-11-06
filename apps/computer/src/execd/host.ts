import type { ChildProcess } from 'node:child_process'
import type { Socket } from 'node:net'
import { PosixHost } from './posix-host.ts'
import type { ProcessSpec, StartedRecord } from './shim.ts'
import { WindowsHost } from './windows-host.ts'

// How this machine's operating system runs a Work process: the shell the
// command runs in, the channel its stdin arrives through, and how a run is
// told alive, signalled and ended. Linux and macOS use a FIFO and the
// command's process group; Windows, which has neither, a named pipe the shim
// serves and the process tree.
export interface ProcessHost {
  // In the shim: starts the command with its stdin channel. ready resolves
  // once writers can reach the channel; close releases it.
  startCommand(spec: ProcessSpec, env: NodeJS.ProcessEnv, stateDir: string, run: number): CommandRun

  // In Talgent Computer: a writer to the run's stdin, or undefined when the
  // channel is gone. A writer that cannot connect emits an error.
  openStdin(stateDir: string, run: number): Socket | undefined
  // Whether the shim of started still runs, not a later process that reused
  // its pid.
  shimAlive(started: StartedRecord, stateDir: string): Promise<boolean>
  // Delivers one of SIGINT, SIGTERM or SIGKILL to the running command.
  signal(started: StartedRecord, signal: NodeJS.Signals, stateDir: string): Promise<void>
  // Ends the command and its shim.
  kill(started: StartedRecord, stateDir: string): Promise<void>
  // Ends what a shim that died without recording the exit left running.
  reap(started: StartedRecord): Promise<void>
  // Variables the shim's own node needs beside PATH.
  shimEnv(): NodeJS.ProcessEnv
}

export interface CommandRun {
  child: ChildProcess
  ready: Promise<void>
  close(): void
}

export function currentHost(): ProcessHost {
  return process.platform === 'win32' ? new WindowsHost() : new PosixHost()
}
