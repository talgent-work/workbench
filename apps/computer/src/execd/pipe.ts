import { watch, type FSWatcher } from 'node:fs'
import type { Socket } from 'node:net'
import { MAX_RECORD_DATA, type LogCursor } from './output-log.ts'
import type { ProcessTable, RunEnvironment } from './processes.ts'

// execd's WebSocket pipe (pty=0) over one process: replay from an offset,
// then live output, stdin and signals, then the exit.

const REPLAY = 3
const STDIN = 0
// Input frames a stream may hold before the process takes them.
export const MAX_PENDING_FRAMES = 256
const POLL_MS = 200

export interface PipeSink {
  text(text: string): void
  binary(frame: Buffer): void
  // Ends the stream from this side: normally, or with status when the data
  // it carried is incomplete.
  end(status?: number): void
  // Resolves when the link can take more output.
  writable(): Promise<void>
  // Aborted when the reader went away.
  readonly closed: AbortSignal
}

export interface PipeInput {
  text(text: string): void
  binary(frame: Buffer): void
}

export interface PipeTarget {
  table: ProcessTable
  workId: string
  id: string
  since: number
  // The environment of a run the attach starts.
  env: RunEnvironment
  // Resolves when a run the attach starts may start, or once signal aborts
  // as the reader goes away or the process is deleted.
  ready: (signal: AbortSignal) => Promise<void>
  log: (message: string) => void
}

function controlFrame(value: unknown): string {
  // execd ends its text frames with a newline.
  return JSON.stringify(value) + '\n'
}

// Opens the pipe, or answers the HTTP status execd refuses the attach with.
export async function openPipe(target: PipeTarget, sink: PipeSink): Promise<{ status: number } | PipeInput> {
  const { table, workId, id } = target
  if (!(await table.exists(workId, id))) return { status: 404 }
  if (!table.acquire(workId, id)) return { status: 409 }
  const deleted = table.entry(workId, id).deleted.signal
  const pump = new Pump(target, sink, deleted)
  const stdin = new StdinWriter(() => table.openStdin(workId, id, pump.attached), () => sink.end())
  // Input and signals wait for the run the attach starts.
  const started = pump.started
  pump.run()
    .catch((error: unknown) => target.log(`process ${id}: ${error instanceof Error ? error.message : String(error)}`))
    .finally(() => {
      stdin.close()
      table.release(workId, id)
      sink.end()
    })
  return {
    text(text) {
      let frame: unknown
      try {
        frame = JSON.parse(text)
      } catch {
        return
      }
      if (typeof frame !== 'object' || frame === null) return
      const { type, signal } = frame as Record<string, unknown>
      if (type === 'signal') {
        if (typeof signal === 'string') void started.then(() => table.signal(workId, id, signal)).catch(() => {})
        return
      }
      sink.text(controlFrame({ type: 'error', error: `unknown frame type ${JSON.stringify(String(type))}`, code: 'INVALID_FRAME' }))
    },
    binary(frame) {
      if (frame.length === 0 || frame[0] !== STDIN) return
      const data = Buffer.from(frame.subarray(1))
      void started.then(() => stdin.write(data))
    },
  }
}

class Pump {
  readonly started: Promise<void>
  // The run the pipe is attached to, once started resolved.
  attached = 0
  private markStarted!: () => void
  private readonly target: PipeTarget
  private readonly sink: PipeSink
  private readonly deleted: AbortSignal
  private wake: (() => void) | undefined

  constructor(target: PipeTarget, sink: PipeSink, deleted: AbortSignal) {
    this.target = target
    this.sink = sink
    this.deleted = deleted
    this.started = new Promise((resolve) => { this.markStarted = resolve })
  }

  private get stopped(): boolean {
    return this.sink.closed.aborted || this.deleted.aborted
  }

  async run(): Promise<void> {
    const { table, workId, id } = this.target
    const cursor = await table.cursor(workId, id)
    const watcher = this.watch()
    const onStop = () => this.wake?.()
    this.sink.closed.addEventListener('abort', onStop)
    this.deleted.addEventListener('abort', onStop)
    try {
      await this.replay(cursor)
      if (this.stopped) return
      this.sink.text(controlFrame({ type: 'connected', session_id: id, mode: 'pipe', role: 'holder' }))
      const phase = await table.phase(workId, id)
      if (phase?.kind === 'running') this.attached = phase.started.run
      else {
        await this.target.ready(AbortSignal.any([this.sink.closed, this.deleted]))
        if (this.stopped) return
        this.attached = await table.start(workId, id, this.target.env)
      }
      this.markStarted()
      await this.live(cursor)
    } finally {
      this.markStarted()
      watcher?.close()
      this.sink.closed.removeEventListener('abort', onStop)
      this.deleted.removeEventListener('abort', onStop)
      await cursor.close()
    }
  }

  // Sends the output from since that the log holds now, merged across
  // stdout and stderr as execd does, each frame tagged with the offset of
  // its first byte.
  private async replay(cursor: LogCursor): Promise<void> {
    const { since } = this.target
    cursor.seek(since)
    let pending: Buffer[] = []
    let pendingOffset = -1
    let pendingBytes = 0
    const flush = () => {
      if (pendingBytes === 0) return
      const header = Buffer.alloc(9)
      header.writeUInt8(REPLAY, 0)
      header.writeBigUInt64BE(BigInt(pendingOffset), 1)
      this.sink.binary(Buffer.concat([header, ...pending]))
      pending = []
      pendingBytes = 0
    }
    for (;;) {
      await this.sink.writable()
      if (this.stopped) return
      const records = await cursor.next()
      if (records.length === 0) break
      for (const record of records) {
        const end = record.offset + record.data.length
        if (end <= since) continue
        const data = record.offset < since ? record.data.subarray(since - record.offset) : record.data
        if (pendingBytes === 0) pendingOffset = end - data.length
        pending.push(data)
        pendingBytes += data.length
        if (pendingBytes >= MAX_RECORD_DATA) flush()
      }
    }
    flush()
  }

  private async live(cursor: LogCursor): Promise<void> {
    const { table, workId, id } = this.target
    for (;;) {
      await this.sink.writable()
      if (this.stopped) return
      if (await this.forward(cursor)) continue
      const phase = await table.phase(workId, id)
      if (phase === undefined) return
      if (phase.kind === 'exited') {
        // The shim records the exit only after the last output.
        while (await this.forward(cursor)) {
          await this.sink.writable()
          if (this.stopped) return
        }
        this.sink.text(controlFrame({ type: 'exit', exit_code: phase.exitCode }))
        return
      }
      await this.changed()
    }
  }

  private async forward(cursor: LogCursor): Promise<boolean> {
    const records = await cursor.next()
    for (const record of records) {
      const frame = Buffer.allocUnsafe(1 + record.data.length)
      frame.writeUInt8(record.stream, 0)
      record.data.copy(frame, 1)
      this.sink.binary(frame)
    }
    return records.length > 0
  }

  private watch(): FSWatcher | undefined {
    try {
      const watcher = watch(this.target.table.dir(this.target.workId, this.target.id), () => this.wake?.())
      watcher.on('error', () => {})
      return watcher
    } catch {
      return undefined
    }
  }

  private changed(): Promise<void> {
    if (this.stopped) return Promise.resolve()
    return new Promise((resolve) => {
      const timer = setTimeout(done, POLL_MS)
      function done() {
        clearTimeout(timer)
        resolve()
      }
      this.wake = () => {
        this.wake = undefined
        done()
      }
    })
  }
}

// Writes stdin into the run's stdin channel without blocking. Input for a
// process that no longer reads it is dropped, as a closed pipe would.
class StdinWriter {
  private socket: Socket | undefined
  private failed = false
  private pending = 0
  private readonly connect: () => Socket | undefined
  private readonly overflow: () => void

  constructor(connect: () => Socket | undefined, overflow: () => void) {
    this.connect = connect
    this.overflow = overflow
  }

  write(data: Buffer): void {
    const socket = this.open()
    if (socket === undefined) return
    this.pending += 1
    if (this.pending > MAX_PENDING_FRAMES) {
      this.overflow()
      return
    }
    socket.write(data, () => { this.pending -= 1 })
  }

  private open(): Socket | undefined {
    if (this.socket !== undefined || this.failed) return this.socket
    const socket = this.connect()
    if (socket === undefined) {
      this.failed = true
      return undefined
    }
    socket.on('error', () => {
      socket.destroy()
      if (this.socket === socket) this.socket = undefined
      this.failed = true
    })
    this.socket = socket
    return socket
  }

  close(): void {
    this.socket?.destroy()
    this.socket = undefined
  }
}
