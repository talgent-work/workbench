import type { PipeInput, PipeSink } from './execd/pipe.ts'
import { MAX_PENDING_FRAMES } from './execd/pipe.ts'
import type { ExecRequest, ExecResponse, StreamOpen } from './execd/service.ts'
import { FILE_CHUNK_BYTES } from './execd/work-files.ts'

// The relay frames of one Gateway connection: exec requests answered by id, and
// pipe and file-read streams multiplexed by a 16-byte stream id on binary
// frames. A file read sends at most FILE_WINDOW_BYTES beyond the bytes the
// Gateway credited back with stream_credit as its reader took them.

export interface TunnelLink {
  text(message: Record<string, unknown>): void
  binary(frame: Buffer): void
  // Bytes queued on the connection, for backpressure.
  buffered(): number
}

export interface TunnelService {
  request(request: ExecRequest): Promise<ExecResponse>
  openStream(open: StreamOpen, sink: PipeSink): Promise<{ status: number } | PipeInput>
}

// Output stops being read while the connection holds this much unsent.
const HIGH_WATER_BYTES = 1024 * 1024
const DRAIN_POLL_MS = 5
// The bytes of a file read in flight to the Gateway or held there.
export const FILE_WINDOW_BYTES = 1024 * 1024

interface Credit {
  // Bytes the file read may still send.
  available: number
  // Resumes a read waiting for credit.
  wake?: () => void
}

interface Stream {
  id: string
  // id lower-cased, as binary frames name it.
  key: string
  prefix: Buffer
  closed: AbortController
  input?: PipeInput
  // A file read's credit; a pipe has none.
  credit?: Credit
  // Frames that arrived before the pipe opened.
  early: ({ text: string } | { binary: Buffer })[]
}

export class Tunnel {
  private readonly service: TunnelService
  private readonly link: TunnelLink
  private readonly log: (message: string) => void
  private readonly streams = new Map<string, Stream>()
  private ended = false

  constructor(service: TunnelService, link: TunnelLink, log: (message: string) => void) {
    this.service = service
    this.link = link
    this.log = log
  }

  text(message: Record<string, unknown>): void {
    switch (message.type) {
      case 'exec_request': return void this.execRequest(message)
      case 'stream_open': return this.streamOpen(message)
      case 'stream_text': {
        const stream = this.streams.get(String(message.stream_id).toLowerCase())
        if (stream !== undefined && typeof message.text === 'string') this.deliver(stream, { text: message.text })
        return
      }
      case 'stream_credit': {
        const stream = this.streams.get(String(message.stream_id).toLowerCase())
        const bytes = message.bytes
        if (stream?.credit === undefined || typeof bytes !== 'number' || !Number.isSafeInteger(bytes) || bytes <= 0) return
        // Never more than a window: the Gateway credits only what it took.
        stream.credit.available = Math.min(FILE_WINDOW_BYTES, stream.credit.available + bytes)
        return this.wake(stream)
      }
      case 'stream_close': {
        const stream = this.streams.get(String(message.stream_id).toLowerCase())
        if (stream !== undefined) this.forget(stream)
        return
      }
    }
  }

  binary(frame: Buffer): void {
    if (frame.length < 16) return
    const stream = this.streams.get(uuidFromBytes(frame.subarray(0, 16)))
    if (stream !== undefined) this.deliver(stream, { binary: frame.subarray(16) })
  }

  // The connection ended: every stream ends with it.
  close(): void {
    this.ended = true
    for (const stream of [...this.streams.values()]) this.forget(stream)
  }

  private async execRequest(message: Record<string, unknown>): Promise<void> {
    const requestId = message.request_id
    let response: ExecResponse
    try {
      response = await this.service.request({
        workId: String(message.work_id ?? ''),
        method: String(message.method ?? ''),
        path: String(message.path ?? ''),
        token: typeof message.token === 'string' ? message.token : '',
        body: message.body,
      })
    } catch (error) {
      this.log(`relay ${String(message.method)} ${String(message.path)} failed: ${error instanceof Error ? error.message : String(error)}`)
      response = { status: 500 }
    }
    if (this.ended) return
    const reply: Record<string, unknown> = { type: 'exec_response', request_id: requestId, status: response.status }
    if (response.body !== undefined) reply.body = response.body
    this.link.text(reply)
  }

  private streamOpen(message: Record<string, unknown>): void {
    const id = String(message.stream_id ?? '')
    const prefix = uuidToBytes(id)
    if (prefix === undefined) return
    const key = id.toLowerCase()
    if (this.streams.has(key)) return this.link.text({ type: 'stream_close', stream_id: id, status: 409 })
    const stream: Stream = { id, key, prefix, closed: new AbortController(), early: [] }
    if (message.kind === 'file') stream.credit = { available: FILE_WINDOW_BYTES }
    this.streams.set(key, stream)
    const workId = String(message.work_id ?? '')
    const token = typeof message.token === 'string' ? message.token : ''
    const since = Number(message.since ?? 0)
    const open: StreamOpen = message.kind === 'file'
      ? { kind: 'file', workId, path: typeof message.path === 'string' ? message.path : '', token }
      : { workId, processId: String(message.process_id ?? ''), since: Number.isFinite(since) ? since : -1, token }
    void this.service
      .openStream(open, this.sink(stream))
      .then(
        (result) => {
          if ('status' in result) return this.refuse(stream, result.status)
          // A stream that ended while the pipe opened delivers nothing.
          if (stream.closed.signal.aborted) return
          stream.input = result
          for (const frame of stream.early.splice(0)) this.deliver(stream, frame)
        },
        (error: unknown) => {
          this.log(`stream ${id} failed: ${error instanceof Error ? error.message : String(error)}`)
          this.refuse(stream, 500)
        },
      )
  }

  private sink(stream: Stream): PipeSink {
    const { link } = this
    return {
      closed: stream.closed.signal,
      text: (text) => {
        if (!stream.closed.signal.aborted) link.text({ type: 'stream_text', stream_id: stream.id, text })
      },
      binary: (frame) => {
        if (stream.closed.signal.aborted) return
        if (stream.credit !== undefined) stream.credit.available -= frame.length
        link.binary(Buffer.concat([stream.prefix, frame]))
      },
      end: (status = 0) => {
        if (stream.closed.signal.aborted) return
        link.text({ type: 'stream_close', stream_id: stream.id, status })
        this.forget(stream)
      },
      writable: async () => {
        while (link.buffered() > HIGH_WATER_BYTES && !stream.closed.signal.aborted) {
          await new Promise((resolve) => setTimeout(resolve, DRAIN_POLL_MS))
        }
        // A file read sends a piece only within its credit.
        const { credit } = stream
        while (credit !== undefined && credit.available < FILE_CHUNK_BYTES && !stream.closed.signal.aborted) {
          await new Promise<void>((resolve) => { credit.wake = resolve })
        }
      },
    }
  }

  private deliver(stream: Stream, frame: { text: string } | { binary: Buffer }): void {
    if (stream.input === undefined) {
      stream.early.push(frame)
      if (stream.early.length > MAX_PENDING_FRAMES) this.sink(stream).end()
      return
    }
    if ('text' in frame) stream.input.text(frame.text)
    else stream.input.binary(frame.binary)
  }

  private refuse(stream: Stream, status: number): void {
    if (stream.closed.signal.aborted) return
    this.link.text({ type: 'stream_close', stream_id: stream.id, status })
    this.forget(stream)
  }

  private forget(stream: Stream): void {
    this.streams.delete(stream.key)
    stream.closed.abort()
    this.wake(stream)
  }

  private wake(stream: Stream): void {
    const wake = stream.credit?.wake
    if (stream.credit !== undefined) stream.credit.wake = undefined
    wake?.()
  }
}

export function uuidToBytes(id: string): Buffer | undefined {
  const hex = id.replaceAll('-', '')
  if (!/^[0-9a-fA-F]{32}$/.test(hex) || !/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(id)) return undefined
  return Buffer.from(hex, 'hex')
}

export function uuidFromBytes(bytes: Buffer): string {
  const hex = bytes.toString('hex')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
