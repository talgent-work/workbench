import type { IncomingMessage } from 'node:http'
import type { Readable } from 'node:stream'
import { confined, openWorkFile, readBounded } from '../execd/work-files.ts'
import { within, workspaceRelative } from '../execd/work-paths.ts'
import { isObject } from '../json.ts'
import { Limit } from '../limit.ts'

// Publication: a Work's MCP tools/call
// of publish_artifact or replace_artifact names a file of the Work by
// source_path, which the platform cannot read on this machine. The local
// proxy alone fills source_content, with the file's bytes; a call it cannot
// complete is answered here with a JSON-RPC error and never forwarded.

export const MAX_SOURCE_BYTES = 64 * 1024 * 1024
// The platform Connector's bound on one MCP request: a source's base64 and
// 1 MiB besides. The proxy reads a request up to it; a longer one passes
// unchanged and the Connector refuses it.
export const MAX_REQUEST_BYTES = base64Length(MAX_SOURCE_BYTES) + 1024 * 1024
const TOOLS = new Set(['publish_artifact', 'replace_artifact'])
// The Work's read-only inputs, which a publication never reads from.
const INPUTS_DIR = 'inputs'
// Requests completed at a time, each holding up to a request's worth of
// files, or of body read past UNHELD_REQUEST_BYTES.
const completions = new Limit(2)
// A request body is read this far before it waits for a completion slot.
export const UNHELD_REQUEST_BYTES = 1024 * 1024

const INVALID_PARAMS = -32602
const INVALID_REQUEST = -32600
const UNREADABLE = 'source_path names no readable file in the Work\'s workspace.'
const TOO_LARGE = 'The source file is larger than 64 MiB. Split it or compress it into a smaller archive.'
const FROM_INPUTS = 'source_path is under inputs/, the Work\'s read-only inputs. Publish a file outside inputs/.'
const OVER_REQUEST = 'The files published in this request exceed its size limit. Publish them in separate requests.'
const NOT_FORWARDED = 'Not forwarded: a publication in this batch was refused.'

// A request body as read so far: the chunks taken, and the rest of the
// request when it was longer than the limit.
export interface ReadBody {
  chunks: Buffer[]
  rest?: Readable
}

// What becomes of a request body with publication calls: forwarded
// completed, or answered here with the JSON-RPC response it gets, when it
// gets one.
export type Publication = { kind: 'forward'; body: Buffer } | { kind: 'answer'; body?: Buffer }

// What the proxy does with an MCP request: forward its body as read, forward
// it completed, or answer it here.
export type Completion = { kind: 'pass'; body: ReadBody } | Publication

// Reads an MCP request's JSON-RPC message and completes its publication
// calls, or answers it here when one is refused. Anything else passes
// unchanged, as does an encoded body or one longer than the Gateway takes,
// which the Gateway refuses. A body longer than UNHELD_REQUEST_BYTES is read
// further only while holding a completion slot, so concurrent large
// requests hold at most two requests' worth of memory.
export async function completeRequest(request: IncomingMessage, root: string): Promise<Completion> {
  const encoding = request.headers['content-encoding']
  if (encoding !== undefined && encoding !== 'identity') return { kind: 'pass', body: { chunks: [], rest: request } }
  const head = await readHead(request, UNHELD_REQUEST_BYTES)
  if (head.rest === undefined) {
    const body = Buffer.concat(head.chunks)
    const calls = publicationCalls(body)
    if (calls === undefined) return { kind: 'pass', body: head }
    return completions.run(() => complete(calls, body, root))
  }
  const rest = head.rest
  return completions.run(async () => {
    const read = head.chunks.reduce((size, chunk) => size + chunk.length, 0)
    const tail = await readHead(rest, MAX_REQUEST_BYTES - read)
    const chunks = [...head.chunks, ...tail.chunks]
    if (tail.rest !== undefined) return { kind: 'pass', body: { chunks, rest: tail.rest } }
    const body = Buffer.concat(chunks)
    const calls = publicationCalls(body)
    if (calls === undefined) return { kind: 'pass', body: { chunks: [body] } }
    return complete(calls, body, root)
  })
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

type Message = Record<string, unknown>
type PublicationArgs = Record<string, unknown>

// A message's publication calls, or undefined when it holds none.
interface Calls {
  parsed: unknown
  batch: boolean
  messages: unknown[]
  calls: { message: Message; args: PublicationArgs }[]
}

function publicationCalls(body: Buffer): Calls | undefined {
  let parsed: unknown
  try {
    parsed = JSON.parse(body.toString('utf8'))
  } catch {
    return undefined
  }
  const batch = Array.isArray(parsed)
  const messages = batch ? (parsed as unknown[]) : [parsed]
  const calls = messages.flatMap((message) => {
    const args = argsOf(message)
    return args === undefined ? [] : [{ message: message as Message, args }]
  })
  return calls.length === 0 ? undefined : { parsed, batch, messages, calls }
}

// What becomes of a message with publication calls; the caller holds a
// completion slot.
async function complete({ parsed, batch, messages, calls }: Calls, body: Buffer, root: string): Promise<Publication> {
  let size = body.length
  // One at a time, so a batch holds one file being read.
  for (const { message, args } of calls) {
    delete args.source_content
    // Without a path the Gateway refuses the call's arguments itself.
    if (typeof args.source_path !== 'string') continue
    const source = await readSource(root, args.source_path, MAX_REQUEST_BYTES - size)
    if ('refusal' in source) return { kind: 'answer', body: refusal(messages, batch, message, source.refusal) }
    const content = source.content.toString('base64')
    args.source_content = content
    size += content.length
  }
  return { kind: 'forward', body: Buffer.from(JSON.stringify(parsed)) }
}

function argsOf(message: unknown): PublicationArgs | undefined {
  if (!isObject(message) || message.method !== 'tools/call' || !isObject(message.params)) return undefined
  const { name, arguments: args } = message.params
  return typeof name === 'string' && TOOLS.has(name) && isObject(args) ? args : undefined
}

// The bytes of the regular file sourcePath names under the Work root, or why
// it is refused. sourcePath is the Cloud's /workspace/..., relative to the
// Work root, or an absolute path into it on this machine; it stays under the
// Work root, symbolic links resolved, and outside its inputs. Its base64
// fits in budget bytes.
async function readSource(root: string, sourcePath: string, budget: number): Promise<{ content: Buffer } | { refusal: string }> {
  if (sourcePath === '') return { refusal: UNREADABLE }
  const file = await openWorkFile(root, workspaceRelative(sourcePath) ?? sourcePath).catch(() => ({ status: 500 }))
  if ('status' in file) return { refusal: UNREADABLE }
  try {
    const inputs = await confined(root, INPUTS_DIR)
    if (inputs !== undefined && within(inputs, file.path)) return { refusal: FROM_INPUTS }
    if (file.size > MAX_SOURCE_BYTES) return { refusal: TOO_LARGE }
    if (base64Length(file.size) > budget) return { refusal: OVER_REQUEST }
    const content = await readBounded(file.handle, MAX_SOURCE_BYTES)
    if (content === undefined) return { refusal: TOO_LARGE }
    if (base64Length(content.length) > budget) return { refusal: OVER_REQUEST }
    return { content }
  } catch {
    return { refusal: UNREADABLE }
  } finally {
    await file.handle.close().catch(() => {})
  }
}

// The response to a message one of whose publication calls was refused:
// that call's error, and for every other request of a batch an error saying
// it was not forwarded. Notifications and responses get none.
function refusal(messages: unknown[], batch: boolean, refused: Message, reason: string): Buffer | undefined {
  const answers = messages.flatMap((message) => {
    if (!isObject(message) || typeof message.method !== 'string' || !('id' in message)) return []
    const error = message === refused ? { code: INVALID_PARAMS, message: reason } : { code: INVALID_REQUEST, message: NOT_FORWARDED }
    return [{ jsonrpc: '2.0', id: message.id, error }]
  })
  if (answers.length === 0) return undefined
  return Buffer.from(JSON.stringify(batch ? answers : answers[0]))
}

function base64Length(bytes: number): number {
  return 4 * Math.ceil(bytes / 3)
}
