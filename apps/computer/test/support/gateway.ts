import { randomUUID } from 'node:crypto'
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { Duplex } from 'node:stream'
import WebSocket, { WebSocketServer } from 'ws'
import { uuidFromBytes, uuidToBytes } from '../../src/tunnel.ts'

// A stand-in Computer Gateway for tests: it approves a device-code
// registration at once, accepts Talgent Computer's connection, opens one
// Work's environment on it, and serves that Work's execd API at
// <origin>/execd over plain HTTP and WebSocket by relaying it as tunnel
// frames.

export interface GatewayOptions {
  workId: string
  accessToken: string
  environment?: Record<string, string>
  // Opens the Work as one on a Model Configuration.
  modelProxy?: boolean
  // What the platform decides on each connection's reconciliation, from the
  // machine's inventory; undefined leaves the round pending. By default
  // nothing is ended or removed.
  reconcile?: (inventory: Inventory) => Omit<Decision, 'round'> | undefined
}

// A pending round's inventory, as GET /reconciliation answers it.
export interface Inventory {
  round: string
  reconciled: boolean
  works: { work_id: string; processes: string[]; exited: string[] }[]
}

// The platform's decision for a round, as POST /reconciliation takes it.
export interface Decision {
  round: string
  remove_works?: string[]
  end_processes?: { work_id: string; process_id: string }[]
}

const REGISTRATION = 'computer.public.v1.RegistrationService'
// The Computer Credential a registration through this Gateway receives.
export const MACHINE_CREDENTIAL = 'machine-credential'

// Marks, among a stream's early frames, that the machine already ended it.
const CLOSED = '\0closed'

interface RelayStream {
  client?: WebSocket
  // Frames from the machine before the client's upgrade completed.
  early: (string | Buffer)[]
  first?: (event: { status: number } | 'data') => void
}

export class Gateway {
  origin = ''
  workRoot = ''
  readonly headers: IncomingMessage['headers'][] = []
  // The machine name the last registration asked for.
  machineName = ''
  // The versions each welcome announces.
  targetVersion = ''
  minimumVersion = ''
  // Whether each welcome announces that the platform reconciles the
  // connection.
  reconciles = true
  private readonly options: GatewayOptions
  private readonly http = createServer((request, response) => void this.serveHttp(request, response))
  private readonly wss = new WebSocketServer({ noServer: true })
  private machine: WebSocket | undefined
  private ready: Promise<void> = new Promise(() => {})
  private markReady: () => void = () => {}
  private readonly requests = new Map<string, (message: Record<string, unknown>) => void>()
  private readonly streams = new Map<string, RelayStream>()
  private readonly fileReads = new Map<string, { chunks: Buffer[]; done: (status: number) => void }>()

  constructor(options: GatewayOptions) {
    this.options = options
    this.expectMachine()
    this.http.on('upgrade', (request, socket, head) => this.upgrade(request, socket, head))
  }

  get execdBase(): string {
    return `${this.origin}/execd`
  }

  async listen(): Promise<void> {
    await new Promise<void>((resolve) => this.http.listen(0, '127.0.0.1', resolve))
    this.origin = `http://127.0.0.1:${(this.http.address() as AddressInfo).port}`
  }

  // Resolves once a machine connected and its Work environment is open.
  connected(): Promise<void> {
    return this.ready
  }

  // Resolves once the connected machine's connection closed, after the
  // Gateway began expecting the next one.
  disconnected(): Promise<void> {
    const machine = this.machine
    if (machine === undefined) return Promise.resolve()
    return new Promise((resolve) => machine.once('close', () => resolve()))
  }

  // Drops the machine's connection as a network loss would, without a close
  // handshake; the machine reconnects on its own.
  dropMachine(): void {
    this.machine?.terminate()
  }

  // Ends the machine's connection as a restarting service does; the machine
  // reconnects.
  restartMachine(): void {
    this.machine?.close(1012, 'restarting')
  }

  async close(): Promise<void> {
    for (const client of this.wss.clients) client.terminate()
    this.machine?.terminate()
    const closed = new Promise<void>((resolve) => this.http.close(() => resolve()))
    // Keep-alive connections would hold close open after a failed check.
    this.http.closeAllConnections()
    await closed
  }

  private expectMachine(): void {
    this.ready = new Promise((resolve) => { this.markReady = resolve })
  }

  private upgrade(request: IncomingMessage, socket: Duplex, head: Buffer): void {
    const url = new URL(request.url ?? '/', 'http://gateway')
    if (url.pathname === '/computer/v1/connect') {
      this.headers.push(request.headers)
      this.wss.handleUpgrade(request, socket, head, (ws) => this.acceptMachine(ws))
      return
    }
    const match = /^\/execd\/pty\/([^/]+)\/ws$/.exec(url.pathname)
    if (match === null || this.machine === undefined) {
      socket.end('HTTP/1.1 503 Unavailable\r\nContent-Length: 0\r\n\r\n')
      return
    }
    const streamId = randomUUID()
    const stream: RelayStream = { early: [] }
    this.streams.set(streamId, stream)
    stream.first = (event) => {
      stream.first = undefined
      if (event !== 'data') {
        this.streams.delete(streamId)
        socket.end(`HTTP/1.1 ${event.status} Refused\r\nContent-Length: 0\r\n\r\n`)
        return
      }
      this.wss.handleUpgrade(request, socket, head, (client) => this.acceptClient(streamId, stream, client))
    }
    this.send({
      type: 'stream_open',
      stream_id: streamId,
      work_id: this.options.workId,
      process_id: match[1],
      since: Number(url.searchParams.get('since') ?? 0),
      token: String(request.headers['x-execd-access-token'] ?? ''),
    })
  }

  // Reads one of the Work's files over the tunnel as the relay serves
  // files/content: the stream's bytes and its closing status.
  readFile(path: string, token = this.options.accessToken): Promise<{ status: number; data: Buffer }> {
    const streamId = randomUUID()
    return new Promise((resolve) => {
      const chunks: Buffer[] = []
      this.fileReads.set(streamId, {
        chunks,
        done: (status) => {
          this.fileReads.delete(streamId)
          resolve({ status, data: Buffer.concat(chunks) })
        },
      })
      this.send({ type: 'stream_open', stream_id: streamId, work_id: this.options.workId, token, kind: 'file', path })
    })
  }

  private acceptClient(streamId: string, stream: RelayStream, client: WebSocket): void {
    stream.client = client
    for (const frame of stream.early.splice(0)) {
      if (frame === CLOSED) client.close(1000)
      else client.send(frame, { binary: Buffer.isBuffer(frame) })
    }
    client.on('message', (data: Buffer, isBinary) => {
      if (isBinary) this.machine?.send(Buffer.concat([uuidToBytes(streamId) ?? Buffer.alloc(16), data]))
      else this.send({ type: 'stream_text', stream_id: streamId, text: data.toString() })
    })
    client.on('close', () => {
      if (this.streams.delete(streamId)) this.send({ type: 'stream_close', stream_id: streamId, status: 0 })
    })
    client.on('error', () => {})
  }

  private acceptMachine(ws: WebSocket): void {
    this.machine?.terminate()
    this.machine = ws
    ws.on('message', (data: Buffer, isBinary) => this.fromMachine(data, isBinary))
    ws.on('close', () => {
      if (this.machine !== ws) return
      this.machine = undefined
      this.expectMachine()
      for (const [id, stream] of this.streams) {
        this.streams.delete(id)
        stream.first?.({ status: 503 })
        stream.client?.close(1001)
      }
      for (const read of this.fileReads.values()) read.done(503)
      for (const resolve of this.requests.values()) resolve({ status: 503 })
      this.requests.clear()
    })
    ws.send(JSON.stringify({
      type: 'welcome',
      computer_id: 'computer-1',
      heartbeat_interval_ms: 30_000,
      target_version: this.targetVersion,
      minimum_version: this.minimumVersion,
      reconciles: this.reconciles,
    }))
    void this.relay('PUT', '/', '', {
      access_token: this.options.accessToken,
      environment: this.options.environment ?? {},
      ...(this.options.modelProxy === true ? { model_proxy: true } : {}),
    }).then((response) => {
      if (response.status !== 200) throw new Error(`environment PUT answered ${String(response.status)}`)
      this.workRoot = String((response.body as { work_root: string }).work_root)
      return this.reconciles ? this.reconcileOnConnect() : undefined
    }).then(() => this.markReady())
  }

  // As Agent does once the machine is reachable again: reads the round's
  // inventory and applies the decision for it.
  private async reconcileOnConnect(): Promise<void> {
    const inventory = await this.inventory()
    const decide = this.options.reconcile ?? (() => ({}))
    const decision = decide(inventory)
    if (decision === undefined) return
    const applied = await this.reconcile({ round: inventory.round, ...decision })
    if (applied.status !== 204) throw new Error(`reconciliation answered ${String(applied.status)}`)
  }

  // GET /reconciliation, which names no Work.
  async inventory(): Promise<Inventory> {
    const response = await this.relay('GET', '/reconciliation', '', undefined, '')
    if (response.status !== 200) throw new Error(`reconciliation inventory answered ${String(response.status)}`)
    return response.body as Inventory
  }

  // POST /reconciliation with a decision.
  async reconcile(decision: Decision): Promise<{ status: number; body?: unknown }> {
    const response = await this.relay('POST', '/reconciliation', '', decision, '')
    return { status: Number(response.status), body: response.body }
  }

  private fromMachine(data: Buffer, isBinary: boolean): void {
    if (isBinary) {
      const read = this.fileReads.get(uuidFromBytes(data.subarray(0, 16)))
      if (read !== undefined) return void read.chunks.push(Buffer.from(data.subarray(16)))
      const stream = this.streams.get(uuidFromBytes(data.subarray(0, 16)))
      if (stream !== undefined) this.toClient(stream, data.subarray(16))
      return
    }
    const message = JSON.parse(data.toString()) as Record<string, unknown>
    if (message.type === 'exec_response') {
      const id = String(message.request_id)
      this.requests.get(id)?.(message)
      this.requests.delete(id)
      return
    }
    const read = this.fileReads.get(String(message.stream_id))
    if (read !== undefined && message.type === 'stream_close') return read.done(Number(message.status))
    const stream = this.streams.get(String(message.stream_id))
    if (stream === undefined) return
    if (message.type === 'stream_text') this.toClient(stream, String(message.text))
    if (message.type === 'stream_close') {
      this.streams.delete(String(message.stream_id))
      const status = Number(message.status)
      if (stream.first !== undefined) stream.first(status === 0 ? 'data' : { status })
      stream.client?.close(1000)
      if (stream.client === undefined && status === 0) stream.early.push(CLOSED)
    }
  }

  private toClient(stream: RelayStream, frame: string | Buffer): void {
    if (stream.client !== undefined) {
      stream.client.send(frame, { binary: Buffer.isBuffer(frame) })
      return
    }
    stream.early.push(frame)
    stream.first?.('data')
  }

  private relay(method: string, path: string, token: string, body?: unknown, workId = this.options.workId): Promise<Record<string, unknown>> {
    const requestId = randomUUID()
    return new Promise((resolve) => {
      this.requests.set(requestId, resolve)
      this.send({ type: 'exec_request', request_id: requestId, work_id: workId, method, path, token, body })
    })
  }

  private async serveHttp(request: IncomingMessage, response: ServerResponse): Promise<void> {
    const url = new URL(request.url ?? '/', 'http://gateway')
    if (url.pathname.startsWith(`/api/${REGISTRATION}/`)) return this.register(url.pathname.slice(REGISTRATION.length + 6), request, response)
    if (!url.pathname.startsWith('/execd/') || this.machine === undefined) {
      response.writeHead(503).end()
      return
    }
    const chunks: Buffer[] = []
    for await (const chunk of request) chunks.push(chunk as Buffer)
    const text = Buffer.concat(chunks).toString()
    const reply = await this.relay(
      request.method ?? 'GET',
      url.pathname.slice('/execd'.length) + url.search,
      String(request.headers['x-execd-access-token'] ?? ''),
      text === '' ? undefined : JSON.parse(text),
    )
    const body = reply.body === undefined ? '' : JSON.stringify(reply.body)
    response.writeHead(Number(reply.status), body === '' ? {} : { 'Content-Type': 'application/json' }).end(body)
  }

  // RegistrationService as the platform answers it, the person approving
  // the code before the machine first asks.
  private async register(method: string, request: IncomingMessage, response: ServerResponse): Promise<void> {
    const chunks: Buffer[] = []
    for await (const chunk of request) chunks.push(chunk as Buffer)
    const body = JSON.parse(Buffer.concat(chunks).toString() || '{}') as Record<string, unknown>
    const answer = (value: unknown) => void response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(value))
    if (method === 'StartRegistration') {
      this.machineName = String(body.machineName ?? '')
      return answer({
        deviceCode: 'device-1',
        userCode: 'ABCD-EFGH',
        verificationUri: `${this.origin}/computers/connect`,
        verificationUriComplete: `${this.origin}/computers/connect?code=ABCD-EFGH`,
        expiresInSeconds: 600,
        intervalSeconds: 1,
      })
    }
    if (method === 'RedeemRegistration' && body.deviceCode === 'device-1') {
      return answer({ status: 'REDEMPTION_STATUS_REGISTERED', computerId: 'computer-1', name: this.machineName, credential: MACHINE_CREDENTIAL })
    }
    response.writeHead(404, { 'Content-Type': 'application/json' }).end(JSON.stringify({ code: 'not_found', message: 'no such registration' }))
  }

  private send(message: Record<string, unknown>): void {
    if (this.machine?.readyState === WebSocket.OPEN) this.machine.send(JSON.stringify(message))
  }
}
