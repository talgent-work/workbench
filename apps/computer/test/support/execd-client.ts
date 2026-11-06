import WebSocket from 'ws'

// A test client of execd's HTTP and WebSocket pipe API, the calls
// internal/pkg/execd makes, so one test drives the real execd and Talgent
// Computer through the Gateway alike.

export type Frame =
  | { type: 'connected'; session_id: string; mode: string; role: string }
  | { type: 'exit'; exit_code: number }
  | { type: 'error'; code: string }
  | { type: 'stdout' | 'stderr'; data: string }
  | { type: 'replay'; offset: number; data: string }

export class ExecdClient {
  readonly base: string
  readonly token: string

  constructor(base: string, token: string) {
    this.base = base
    this.token = token
  }

  async call(method: string, path: string, body?: unknown, token = this.token): Promise<{ status: number; body: unknown }> {
    const response = await fetch(this.base + path, {
      method,
      headers: { 'X-EXECD-ACCESS-TOKEN': token, 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await response.text()
    let parsed: unknown = undefined
    try {
      parsed = text === '' ? undefined : JSON.parse(text)
    } catch {
      parsed = text
    }
    return { status: response.status, body: parsed }
  }

  async create(cwd: string, command: string): Promise<string> {
    const response = await this.call('POST', '/pty', { cwd, command })
    if (response.status !== 201) throw new Error(`create answered ${response.status}`)
    return (response.body as { session_id: string }).session_id
  }

  async status(id: string): Promise<{ status: number; running?: boolean; offset?: number }> {
    const response = await this.call('GET', `/pty/${id}`)
    const body = response.body as { running?: boolean; output_offset?: number } | undefined
    return { status: response.status, running: body?.running, offset: body?.output_offset }
  }

  // Opens a pipe; a refused attach resolves with its HTTP status.
  attach(id: string, since: number): Promise<Pipe | { refused: number }> {
    const url = `${this.base.replace(/^http/, 'ws')}/pty/${id}/ws?pty=0&since=${since}`
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url, { headers: { 'X-EXECD-ACCESS-TOKEN': this.token } })
      const pipe = new Pipe(ws)
      ws.once('unexpected-response', (_request, response) => {
        response.resume()
        resolve({ refused: response.statusCode ?? 0 })
        ws.terminate()
      })
      ws.once('open', () => resolve(pipe))
      ws.once('error', (error) => reject(error))
    })
  }

  // Attaches, retrying while a just-detached pipe still holds the process.
  async attachWhenFree(id: string, since: number, timeoutMs = 5000): Promise<Pipe> {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      const result = await this.attach(id, since)
      if (result instanceof Pipe) return result
      if (result.refused !== 409 || Date.now() > deadline) throw new Error(`attach refused with ${result.refused}`)
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
  }
}

export class Pipe {
  readonly frames: Frame[] = []
  closed = false
  private readonly ws: WebSocket
  private waiters: (() => void)[] = []
  private read = 0

  constructor(ws: WebSocket) {
    this.ws = ws
    ws.on('message', (data: Buffer, isBinary) => {
      this.frames.push(isBinary ? decodeBinary(data) : (JSON.parse(data.toString()) as Frame))
      this.notify()
    })
    ws.on('close', () => {
      this.closed = true
      this.notify()
    })
    ws.on('error', () => {})
  }

  private notify(): void {
    for (const waiter of this.waiters.splice(0)) waiter()
  }

  // The next frame, or undefined once the pipe closed.
  async next(timeoutMs = 10_000): Promise<Frame | undefined> {
    const deadline = Date.now() + timeoutMs
    while (this.read >= this.frames.length) {
      if (this.closed) return undefined
      if (Date.now() > deadline) throw new Error(`no frame within ${timeoutMs}ms; got ${JSON.stringify(this.frames)}`)
      await new Promise<void>((resolve) => {
        const timer = setTimeout(resolve, 100)
        this.waiters.push(() => {
          clearTimeout(timer)
          resolve()
        })
      })
    }
    return this.frames[this.read++]
  }

  // Reads frames until one matches; returns every frame read on the way.
  async until(match: (frame: Frame, seen: Frame[]) => boolean, timeoutMs = 10_000): Promise<Frame[]> {
    const seen: Frame[] = []
    for (;;) {
      const frame = await this.next(timeoutMs)
      if (frame === undefined) throw new Error(`pipe closed before the expected frame; got ${JSON.stringify(seen)}`)
      seen.push(frame)
      if (match(frame, seen)) return seen
    }
  }

  // Waits until the remote side closes the pipe.
  async ended(timeoutMs = 10_000): Promise<void> {
    const deadline = Date.now() + timeoutMs
    while (!this.closed) {
      if (Date.now() > deadline) throw new Error('pipe did not close')
      await new Promise((resolve) => setTimeout(resolve, 20))
    }
  }

  stdin(text: string): void {
    this.ws.send(Buffer.concat([Buffer.from([0]), Buffer.from(text)]))
  }

  signal(signal: string): void {
    this.ws.send(JSON.stringify({ type: 'signal', signal }))
  }

  sendText(text: string): void {
    this.ws.send(text)
  }

  // Detaches at once: execd does not answer a WebSocket close handshake.
  detach(): void {
    this.ws.terminate()
  }
}

function decodeBinary(data: Buffer): Frame {
  switch (data[0]) {
    case 1: return { type: 'stdout', data: data.subarray(1).toString() }
    case 2: return { type: 'stderr', data: data.subarray(1).toString() }
    case 3: return { type: 'replay', offset: Number(data.readBigUInt64BE(1)), data: data.subarray(9).toString() }
    default: throw new Error(`unexpected binary frame ${data[0]}`)
  }
}

export function output(frames: Frame[], ...types: Frame['type'][]): string {
  return frames
    .filter((frame) => types.includes(frame.type))
    .map((frame) => ('data' in frame ? frame.data : ''))
    .join('')
}
