import type { IncomingMessage } from 'node:http'
import { isObject } from '../json.ts'
import { holdingCompletion, MAX_REQUEST_BYTES, readSource, UNHELD_REQUEST_BYTES } from './publication-source.ts'
import { readHead, type ReadBody } from './request-body.ts'

// Publication: a Work's MCP tools/call
// of publish_artifact or replace_artifact names a file of the Work by
// source_path, which the platform cannot read on this machine. The local
// proxy alone fills source_content, with the file's bytes; a call it cannot
// complete is answered here with a JSON-RPC error and never forwarded.

const TOOLS = new Set(['publish_artifact', 'replace_artifact'])

const INVALID_PARAMS = -32602
const INVALID_REQUEST = -32600
const NOT_FORWARDED = 'Not forwarded: a publication in this batch was refused.'

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
    return holdingCompletion(() => complete(calls, body, root))
  }
  const rest = head.rest
  return holdingCompletion(async () => {
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
    const source = await readSource(root, args.source_path, 'source_path', MAX_REQUEST_BYTES - size)
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
