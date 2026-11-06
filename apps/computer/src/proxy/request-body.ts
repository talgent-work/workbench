import type { Readable } from 'node:stream'

// A request body as read so far: the chunks taken, and the rest of the
// request when it was longer than the limit.
export interface ReadBody {
  chunks: Buffer[]
  rest?: Readable
}

// Reads a body up to limit bytes; past that it stops reading and hands back
// what it took with the stream itself for the rest. A stream that closes
// before its end fails the read.
// A request body that sends nothing for this long is abandoned, so a
// stalled client never holds a completion slot; an upstream answer reads
// with Infinity, its pace being the tool call's.
export const BODY_IDLE_MS = 30_000

export function readHead(stream: Readable, limit: number, idleMs = BODY_IDLE_MS): Promise<ReadBody> {
  return new Promise((resolve, reject) => {
    if (stream.destroyed) return reject(new Error('the request closed before its end'))
    const chunks: Buffer[] = []
    let size = 0
    let idle: NodeJS.Timeout | undefined
    const arm = () => {
      clearTimeout(idle)
      if (!Number.isFinite(idleMs)) return
      idle = setTimeout(() => {
        cleanup()
        stream.destroy()
        reject(new Error('the body stalled'))
      }, idleMs)
    }
    const cleanup = () => {
      clearTimeout(idle)
      stream.off('data', onData)
      stream.off('end', onEnd)
      stream.off('error', onError)
      stream.off('close', onClose)
    }
    const onData = (chunk: Buffer) => {
      arm()
      chunks.push(chunk)
      size += chunk.length
      if (size > limit) {
        stream.pause()
        cleanup()
        resolve({ chunks, rest: stream })
      }
    }
    const onEnd = () => {
      cleanup()
      resolve({ chunks })
    }
    const onError = (error: Error) => {
      cleanup()
      reject(error)
    }
    const onClose = () => onError(new Error('the request closed before its end'))
    stream.on('data', onData)
    stream.on('end', onEnd)
    stream.on('error', onError)
    stream.on('close', onClose)
    // A stream paused by an earlier read flows again.
    arm()
    stream.resume()
  })
}
