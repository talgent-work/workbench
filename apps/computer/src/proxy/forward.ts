import http, { type IncomingHttpHeaders, type IncomingMessage, type OutgoingHttpHeaders, type ServerResponse } from 'node:http'
import https from 'node:https'
import { pipeline, type Readable } from 'node:stream'

// Relays one request to its upstream and the response back, both streamed as
// they arrive, so a model's server-sent events reach the process event by
// event.

// connectMs bounds reaching the upstream; a stream may then stay quiet up to
// idleMs between bytes, however long it lasts in all.
export interface Timeouts {
  connectMs: number
  idleMs: number
}
export const DEFAULT_TIMEOUTS: Timeouts = { connectMs: 10_000, idleMs: 10 * 60_000 }
// Connections to one upstream at a time; further requests queue.
const MAX_SOCKETS = 64

const agents = {
  'http:': new http.Agent({ keepAlive: true, maxSockets: MAX_SOCKETS }),
  'https:': new https.Agent({ keepAlive: true, maxSockets: MAX_SOCKETS }),
}

// Headers that belong to one connection, not to the request it carries.
const HOP_BY_HOP = new Set([
  'connection', 'keep-alive', 'proxy-connection', 'transfer-encoding', 'te', 'trailer', 'upgrade',
  'proxy-authenticate', 'proxy-authorization',
])
// What a process might present as its own credentials, the proxy token
// included; the proxy adds the Work's own.
const CLIENT_CREDENTIALS = new Set(['authorization', 'x-api-key', 'cookie', 'host'])
// An upstream's cookies are for no process here.
const UPSTREAM_ONLY = new Set(['set-cookie'])

function connectionListed(headers: IncomingHttpHeaders): Set<string> {
  const value = headers.connection
  return new Set((value ?? '').split(',').map((name) => name.trim().toLowerCase()).filter((name) => name !== ''))
}

// The request headers a process sent, without connection headers, its
// credentials or any X-Talgent-* header it may have made up.
export function requestHeaders(incoming: IncomingHttpHeaders): OutgoingHttpHeaders {
  const listed = connectionListed(incoming)
  const headers: OutgoingHttpHeaders = {}
  for (const [name, value] of Object.entries(incoming)) {
    if (value === undefined || HOP_BY_HOP.has(name) || listed.has(name) || CLIENT_CREDENTIALS.has(name)) continue
    if (name.startsWith('x-talgent-')) continue
    headers[name] = value
  }
  return headers
}

function responseHeaders(incoming: IncomingHttpHeaders): OutgoingHttpHeaders {
  const listed = connectionListed(incoming)
  const headers: OutgoingHttpHeaders = {}
  for (const [name, value] of Object.entries(incoming)) {
    if (value !== undefined && !HOP_BY_HOP.has(name) && !listed.has(name) && !UPSTREAM_ONLY.has(name)) headers[name] = value
  }
  return headers
}

export function answerJson(response: ServerResponse, status: number, body: { code: string; message: string }): void {
  if (response.headersSent) {
    response.destroy()
    return
  }
  const text = JSON.stringify(body)
  response.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(text) }).end(text)
}

export function forward(
  request: IncomingMessage,
  response: ServerResponse,
  target: URL,
  headers: OutgoingHttpHeaders,
  // body: what was read of the request's body already, and the rest of it
  // still to stream; absent, the request streams as it arrives.
  // relay: how the upstream's response reaches the process; absent, as it is.
  options: { timeouts: Timeouts; fail: (reason: string) => void; body?: { chunks: Buffer[]; rest?: Readable }; relay?: ResponseRelay },
): void {
  const { timeouts, fail, body, relay = relayAsIs } = options
  const secure = target.protocol === 'https:'
  const upstream = (secure ? https : http).request(target, {
    method: request.method,
    headers,
    agent: agents[secure ? 'https:' : 'http:'],
    timeout: timeouts.idleMs,
  })
  const connectTimer = setTimeout(() => upstream.destroy(new ProxyFailure('connect timeout')), timeouts.connectMs)
  upstream.once('socket', (socket) => {
    if (upstream.reusedSocket) return clearTimeout(connectTimer)
    socket.once(secure ? 'secureConnect' : 'connect', () => clearTimeout(connectTimer))
  })
  upstream.on('timeout', () => upstream.destroy(new ProxyFailure('idle timeout')))
  upstream.on('error', (error) => {
    clearTimeout(connectTimer)
    fail(reasonOf(error))
    answerJson(response, 502, { code: 'UPSTREAM_UNAVAILABLE', message: 'the upstream did not answer' })
  })
  upstream.on('response', (answer) => {
    clearTimeout(connectTimer)
    relay(answer, answer.statusCode ?? 502, responseHeaders(answer.headers), response)
  })
  // The process went away: so does the upstream request.
  response.on('close', () => {
    if (!response.writableFinished) upstream.destroy()
  })
  request.on('error', () => upstream.destroy())
  if (body === undefined) return void request.pipe(upstream)
  for (const chunk of body.chunks) upstream.write(chunk)
  if (body.rest === undefined) upstream.end()
  else body.rest.pipe(upstream)
}

// Relays an upstream response, its headers already filtered, to the process.
export type ResponseRelay = (answer: IncomingMessage, status: number, headers: OutgoingHttpHeaders, response: ServerResponse) => void

export const relayAsIs: ResponseRelay = (answer, status, headers, response) => {
  response.writeHead(status, headers)
  response.flushHeaders()
  pipeline(answer, response, (error) => {
    if (error) response.destroy()
  })
}

class ProxyFailure extends Error {}

// Why the upstream failed, without anything the request carried.
function reasonOf(error: unknown): string {
  if (error instanceof ProxyFailure) return error.message
  const code = (error as NodeJS.ErrnoException | undefined)?.code
  return typeof code === 'string' ? code : 'connection failed'
}
