import { setTimeout as delay } from 'node:timers/promises'
import WebSocket from 'ws'
import { backoffDelayMs } from './backoff.ts'
import type { TunnelLink } from './tunnel.ts'

// Close codes the server ends a connection with when retrying cannot help.
export const CLOSE_CREDENTIAL_REJECTED = 4401
export const CLOSE_COMPUTER_REMOVED = 4403
// Another connection with this Computer's credential took over.
export const CLOSE_REPLACED = 4409

// Tells the service this run saves a rotated credential and answers the
// credential frame; it is sent only when the run can save one.
export const HEADER_CREDENTIAL_ROTATION = 'X-Talgent-Credential-Rotation'

export const DEFAULT_HEARTBEAT_MS = 30_000
// Silence for this many heartbeat intervals means the link is dead.
const MISSED_HEARTBEATS = 3
// A connection that stayed up this long resets the backoff.
const DEFAULT_STABLE_AFTER_MS = 60_000
const HANDSHAKE_TIMEOUT_MS = 15_000
const CLOSE_GRACE_MS = 3_000

export type ConnectionOutcome =
  | { kind: 'stopped' }
  | { kind: 'credential_rejected' }
  | { kind: 'computer_removed' }
  | { kind: 'replaced' }

type AttemptResult = ConnectionOutcome | { kind: 'dropped'; reason: string; upMs: number }

// The relay traffic of one connection: every frame that is not the
// connection's own, until the connection closes.
export interface RelaySession {
  text(message: Record<string, unknown>): void
  binary(frame: Buffer): void
  close(): void
}

export interface ConnectionOptions {
  url: string
  // Read again for every attempt, so a reconnection reports what changed.
  headers: Record<string, string> | (() => Record<string, string>)
  signal: AbortSignal
  log: (message: string) => void
  backoff?: (attempt: number) => number
  stableAfterMs?: number
  defaultHeartbeatMs?: number
  relay?: (link: TunnelLink) => RelaySession
  // Contact from the service: the upgrade completing, any frame, a ping.
  heard?: () => void
  // Each connection's welcome, with the versions the platform announces.
  welcomed?: (welcome: Welcome) => void
  // A rotated Computer Credential the service sent; it resolves once the
  // credential is saved, and the service is told whether it was. Without
  // it the service is not told this run supports rotation.
  credentialIssued?: (issued: IssuedCredential) => Promise<void>
}

// The successor of this Computer's credential, which the next connection
// presents once it is saved.
export interface IssuedCredential {
  credential: string
  // ISO 8601, or null when the service set no expiry.
  expiresAt: string | null
}

// What the platform says as a connection opens; a version it does not
// announce is "".
export interface Welcome {
  computerId: string
  targetVersion: string
  minimumVersion: string
  // The platform reconciles this connection before anything starts here.
  reconciles: boolean
}

// Keeps one connection to the server until it is stopped through the signal
// or the server says this Computer may no longer connect. Every other
// failure is retried with backoff.
export async function runConnection(options: ConnectionOptions): Promise<ConnectionOutcome> {
  const backoff = options.backoff ?? ((attempt: number) => backoffDelayMs(attempt))
  const stableAfterMs = options.stableAfterMs ?? DEFAULT_STABLE_AFTER_MS
  let attempt = 0
  for (;;) {
    if (options.signal.aborted) return { kind: 'stopped' }
    const result = await connectOnce(options)
    if (result.kind !== 'dropped') return result
    if (result.upMs >= stableAfterMs) attempt = 0
    const waitMs = backoff(attempt)
    attempt += 1
    options.log(`disconnected (${result.reason}); retrying in ${formatSeconds(waitMs)}`)
    try {
      await delay(waitMs, undefined, { signal: options.signal })
    } catch {
      return { kind: 'stopped' }
    }
  }
}

function connectOnce(options: ConnectionOptions): Promise<AttemptResult> {
  return new Promise((resolve) => {
    const headers = { ...(typeof options.headers === 'function' ? options.headers() : options.headers) }
    if (options.credentialIssued !== undefined) headers[HEADER_CREDENTIAL_ROTATION] = 'supported'
    const ws = new WebSocket(options.url, { headers, handshakeTimeout: HANDSHAKE_TIMEOUT_MS })
    let heartbeatMs = options.defaultHeartbeatMs ?? DEFAULT_HEARTBEAT_MS
    let openedAt: number | undefined
    let lastSeen = Date.now()
    let rejectedStatus: number | undefined
    let lastError = ''
    let stopping = false
    let heartbeat: NodeJS.Timeout | undefined
    let closeGrace: NodeJS.Timeout | undefined
    let relay: RelaySession | undefined

    const startHeartbeat = () => {
      clearInterval(heartbeat)
      heartbeat = setInterval(() => {
        if (Date.now() - lastSeen > heartbeatMs * MISSED_HEARTBEATS) {
          lastError = 'no heartbeat from server'
          ws.terminate()
          return
        }
        send(ws, { type: 'heartbeat' })
      }, heartbeatMs)
    }

    const onAbort = () => {
      stopping = true
      if (ws.readyState === WebSocket.OPEN) {
        ws.close(1000)
        closeGrace = setTimeout(() => ws.terminate(), CLOSE_GRACE_MS)
      } else {
        ws.terminate()
      }
    }
    options.signal.addEventListener('abort', onAbort, { once: true })

    ws.on('unexpected-response', (_request, response) => {
      rejectedStatus = response.statusCode
      response.resume()
      ws.terminate()
    })
    ws.on('open', () => {
      openedAt = Date.now()
      lastSeen = openedAt
      options.heard?.()
      startHeartbeat()
      relay = options.relay?.({
        text: (message) => send(ws, message),
        binary: (frame) => {
          if (ws.readyState === WebSocket.OPEN) ws.send(frame, { binary: true })
        },
        buffered: () => ws.bufferedAmount,
      })
    })
    ws.on('ping', () => {
      lastSeen = Date.now()
      options.heard?.()
    })
    ws.on('message', (data, isBinary) => {
      lastSeen = Date.now()
      options.heard?.()
      const bytes = toBuffer(data)
      if (isBinary) return relay?.binary(bytes)
      const message = parse(bytes.toString())
      if (message === undefined || message.type === 'heartbeat_ack') return
      if (message.type === 'credential') return saveIssued(ws, message, options)
      if (message.type !== 'welcome') return relay?.text(message)
      const interval = Number(message.heartbeat_interval_ms)
      if (Number.isFinite(interval) && interval > 0) heartbeatMs = interval
      startHeartbeat()
      const computerId = String(message.computer_id ?? 'unknown')
      options.log(`connected as ${computerId}`)
      options.welcomed?.({ computerId, targetVersion: text(message.target_version), minimumVersion: text(message.minimum_version), reconciles: message.reconciles === true })
    })
    ws.on('error', (error) => {
      lastError = error.message
    })
    ws.on('close', (code, reason) => {
      clearInterval(heartbeat)
      clearTimeout(closeGrace)
      relay?.close()
      options.signal.removeEventListener('abort', onAbort)
      if (stopping) return resolve({ kind: 'stopped' })
      if (code === CLOSE_CREDENTIAL_REJECTED || rejectedStatus === 401) return resolve({ kind: 'credential_rejected' })
      if (code === CLOSE_COMPUTER_REMOVED || rejectedStatus === 403) return resolve({ kind: 'computer_removed' })
      if (code === CLOSE_REPLACED) return resolve({ kind: 'replaced' })
      const upMs = openedAt === undefined ? 0 : Date.now() - openedAt
      resolve({ kind: 'dropped', reason: describeDrop(code, reason.toString(), rejectedStatus, lastError), upMs })
    })
  })
}

// Saves a rotated credential and tells the service whether it did; the
// service confirms it only after credential_saved, so until then either
// credential connects. The credential itself is never logged.
function saveIssued(ws: WebSocket, message: Record<string, unknown>, options: ConnectionOptions): void {
  const credential = text(message.credential)
  const expiresMs = Number(message.expires_at_unix_ms)
  const issued: IssuedCredential = { credential, expiresAt: Number.isFinite(expiresMs) && expiresMs > 0 ? new Date(expiresMs).toISOString() : null }
  const save = credential === '' || options.credentialIssued === undefined ? Promise.reject(new Error('no credential to save')) : options.credentialIssued(issued)
  save.then(
    () => {
      options.log('saved the rotated Computer Credential')
      send(ws, { type: 'credential_saved' })
    },
    (error: unknown) => {
      options.log(`could not save the rotated Computer Credential: ${error instanceof Error ? error.message : String(error)}`)
      send(ws, { type: 'credential_not_saved' })
    },
  )
}

function describeDrop(code: number, reason: string, status: number | undefined, error: string): string {
  if (status !== undefined) return `server answered HTTP ${status}`
  if (error !== '') return error
  return reason === '' ? `close ${code}` : `close ${code}: ${reason}`
}

function send(ws: WebSocket, message: unknown): void {
  if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(message))
}

function toBuffer(data: WebSocket.RawData): Buffer {
  if (Buffer.isBuffer(data)) return data
  if (Array.isArray(data)) return Buffer.concat(data)
  return Buffer.from(data)
}

function parse(text: string): Record<string, unknown> | undefined {
  try {
    const value: unknown = JSON.parse(text)
    return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : undefined
  } catch {
    return undefined
  }
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function formatSeconds(ms: number): string {
  return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)}s`
}

// The connect endpoint on the same host as the server, wss for https.
export function connectUrl(server: string): string {
  const url = new URL('/computer/v1/connect', server)
  if (url.protocol === 'https:') url.protocol = 'wss:'
  else if (url.protocol === 'http:') url.protocol = 'ws:'
  else throw new Error(`unsupported server URL ${server}`)
  return url.toString()
}
