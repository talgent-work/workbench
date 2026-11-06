import { createServer, request as httpRequest, type IncomingHttpHeaders, type OutgoingHttpHeaders, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'

// A stand-in model vendor and Talgent MCP endpoint behind the local proxy: it
// records what reaches it and answers /anthropic/v1/messages with
// server-sent events whose second event waits for release().

export interface Seen {
  method: string
  url: string
  headers: IncomingHttpHeaders
  body: string
}

export class Upstream {
  origin = ''
  readonly seen: Seen[] = []
  // Answers a request itself when it returns true.
  answer?: (url: string, response: ServerResponse) => boolean
  private releaseStream: () => void = () => {}
  private held: Promise<void> = Promise.resolve()
  private readonly server = createServer((request, response) => {
    const chunks: Buffer[] = []
    request.on('data', (chunk: Buffer) => chunks.push(chunk))
    request.on('end', () => {
      this.seen.push({ method: request.method ?? '', url: request.url ?? '', headers: request.headers, body: Buffer.concat(chunks).toString() })
      if (this.answer?.(request.url ?? '', response) === true) return
      if (request.url?.startsWith('/anthropic/v1/messages?stream')) {
        response.writeHead(200, { 'Content-Type': 'text/event-stream', Connection: 'keep-alive' })
        response.write('event: message_start\ndata: {}\n\n')
        void this.held.then(() => response.end('event: message_stop\ndata: {}\n\n'))
        return
      }
      response.writeHead(200, { 'Content-Type': 'application/json', 'X-Upstream': 'yes', 'Set-Cookie': 'vendor=1' }).end('{"ok":true}')
    })
  })

  async listen(): Promise<void> {
    await new Promise<void>((resolve) => this.server.listen(0, '127.0.0.1', resolve))
    this.origin = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`
  }

  // The next stream holds its last event until release().
  hold(): void {
    this.held = new Promise((resolve) => { this.releaseStream = resolve })
  }

  release(): void {
    this.releaseStream()
  }

  last(): Seen {
    const seen = this.seen.at(-1)
    if (seen === undefined) throw new Error('nothing reached the upstream')
    return seen
  }

  async close(): Promise<void> {
    this.release()
    const closed = new Promise<void>((resolve) => this.server.close(() => resolve()))
    this.server.closeAllConnections()
    await closed
  }
}

export interface Answer {
  status: number
  headers: IncomingHttpHeaders
  body: string
}

// One request through node:http, so headers a browser would forbid travel too.
// rawPath is sent as it is, dot segments included.
export function send(url: string, options: { method?: string; headers?: OutgoingHttpHeaders; body?: string; rawPath?: string } = {}): Promise<Answer> {
  return new Promise((resolve, reject) => {
    const target = new URL(url)
    const outgoing = httpRequest({
      host: target.hostname,
      port: target.port,
      path: options.rawPath ?? target.pathname + target.search,
      method: options.method ?? 'GET',
      headers: options.headers,
    }, (response) => {
      const chunks: Buffer[] = []
      response.on('data', (chunk: Buffer) => chunks.push(chunk))
      response.on('end', () => resolve({ status: response.statusCode ?? 0, headers: response.headers, body: Buffer.concat(chunks).toString() }))
      response.on('error', reject)
    })
    outgoing.on('error', reject)
    outgoing.end(options.body)
  })
}
