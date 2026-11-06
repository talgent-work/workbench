import assert from 'node:assert/strict'
import { createServer, type IncomingMessage } from 'node:http'
import type { AddressInfo } from 'node:net'
import { afterEach, test } from 'node:test'
import { WebSocketServer, type WebSocket } from 'ws'
import { connectUrl, runConnection } from '../src/connection.ts'

type Handler = (socket: WebSocket, request: IncomingMessage, attempt: number) => void

const cleanups: (() => Promise<void>)[] = []
afterEach(async () => {
  while (cleanups.length > 0) await cleanups.pop()?.()
})

// A local server that runs handler for each accepted connection, or answers
// the upgrade with rejectStatus.
async function serve(handler: Handler, rejectStatus?: number) {
  const http = createServer()
  const wss = new WebSocketServer({ noServer: true })
  let attempts = 0
  const requests: IncomingMessage[] = []
  http.on('upgrade', (request, socket, head) => {
    requests.push(request)
    attempts += 1
    if (rejectStatus !== undefined) {
      socket.end(`HTTP/1.1 ${rejectStatus} Rejected\r\nConnection: close\r\nContent-Length: 0\r\n\r\n`)
      return
    }
    const attempt = attempts
    wss.handleUpgrade(request, socket, head, (ws) => handler(ws, request, attempt))
  })
  await new Promise<void>((resolve) => http.listen(0, '127.0.0.1', resolve))
  cleanups.push(async () => {
    for (const client of wss.clients) client.terminate()
    await new Promise<void>((resolve) => http.close(() => resolve()))
  })
  const { port } = http.address() as AddressInfo
  return { url: `ws://127.0.0.1:${port}/computer/v1/connect`, requests, attempts: () => attempts }
}

function options(url: string, signal = new AbortController().signal) {
  const logs: string[] = []
  return {
    logs,
    opts: {
      url,
      headers: { Authorization: 'Bearer secret', 'X-Talgent-Computer-Version': '0.1.0' },
      signal,
      log: (line: string) => logs.push(line),
      backoff: () => 10,
    },
  }
}

test('4401 ends the connection as credential rejected', async () => {
  const server = await serve((ws) => ws.close(4401, 'credential expired'))
  const { opts } = options(server.url)
  assert.deepEqual(await runConnection(opts), { kind: 'credential_rejected' })
  assert.equal(server.requests[0].headers.authorization, 'Bearer secret')
  assert.equal(server.requests[0].headers['x-talgent-computer-version'], '0.1.0')
  assert.equal(server.requests[0].headers['x-talgent-credential-rotation'], undefined, 'a run that cannot save a rotated credential does not claim rotation')
})

test('4409 ends the connection as replaced by another run', async () => {
  const server = await serve((ws) => ws.close(4409))
  assert.deepEqual(await runConnection(options(server.url).opts), { kind: 'replaced' })
})

test('4403 ends the connection as Computer removed', async () => {
  const server = await serve((ws) => ws.close(4403))
  assert.deepEqual(await runConnection(options(server.url).opts), { kind: 'computer_removed' })
})

test('upgrade answered 401 or 403 ends without retry', async () => {
  const unauthorized = await serve(() => {}, 401)
  assert.deepEqual(await runConnection(options(unauthorized.url).opts), { kind: 'credential_rejected' })
  assert.equal(unauthorized.attempts(), 1)
  const forbidden = await serve(() => {}, 403)
  assert.deepEqual(await runConnection(options(forbidden.url).opts), { kind: 'computer_removed' })
})

test('other closes are retried until a terminal close', async () => {
  const server = await serve((ws, _request, attempt) => {
    if (attempt === 1) ws.close(1011, 'internal')
    else if (attempt === 2) ws.terminate()
    else ws.close(4401)
  })
  const { opts, logs } = options(server.url)
  assert.deepEqual(await runConnection(opts), { kind: 'credential_rejected' })
  assert.equal(server.attempts(), 3)
  assert.equal(logs.filter((line) => line.includes('retrying in')).length, 2)
})

test('upgrade answered 503 is retried', async () => {
  const server = await serve(() => {}, 503)
  const controller = new AbortController()
  const { opts } = options(server.url, controller.signal)
  const outcome = runConnection({
    ...opts,
    log: (line) => {
      if (server.attempts() >= 2) controller.abort()
      opts.log(line)
    },
  })
  assert.deepEqual(await outcome, { kind: 'stopped' })
  assert.ok(server.attempts() >= 2)
})

test('heartbeats after welcome, and abort closes with 1000', async () => {
  const controller = new AbortController()
  let closeCode = 0
  const heartbeats: string[] = []
  const server = await serve((ws) => {
    ws.send(JSON.stringify({ type: 'welcome', computer_id: 'c-1', heartbeat_interval_ms: 20 }))
    ws.send(JSON.stringify({ type: 'future_relay', payload: 1 }))
    ws.on('message', (data) => {
      heartbeats.push(data.toString())
      ws.send(JSON.stringify({ type: 'heartbeat_ack' }))
      if (heartbeats.length === 3) controller.abort()
    })
    ws.on('close', (code) => { closeCode = code })
  })
  const { opts, logs } = options(server.url, controller.signal)
  assert.deepEqual(await runConnection(opts), { kind: 'stopped' })
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(closeCode, 1000)
  assert.deepEqual(heartbeats, Array(3).fill('{"type":"heartbeat"}'))
  assert.ok(logs.includes('connected as c-1'))
})

test('each welcome passes on the versions the platform announces', async () => {
  const controller = new AbortController()
  const server = await serve((ws, _request, attempt) => {
    if (attempt === 1) ws.send(JSON.stringify({ type: 'welcome', computer_id: 'c-1', heartbeat_interval_ms: 1000, target_version: '0.3.0', minimum_version: '0.2.0', reconciles: true }))
    else ws.send(JSON.stringify({ type: 'welcome', computer_id: 'c-1', heartbeat_interval_ms: 1000 }))
    if (attempt === 1) setTimeout(() => ws.terminate(), 10)
    else setTimeout(() => controller.abort(), 10)
  })
  const welcomes: unknown[] = []
  const { opts } = options(server.url, controller.signal)
  assert.deepEqual(await runConnection({ ...opts, welcomed: (welcome) => welcomes.push(welcome) }), { kind: 'stopped' })
  assert.deepEqual(welcomes, [
    { computerId: 'c-1', targetVersion: '0.3.0', minimumVersion: '0.2.0', reconciles: true },
    { computerId: 'c-1', targetVersion: '', minimumVersion: '', reconciles: false },
  ], 'an older platform announces none and reconciles nothing')
})

test('a silent server is dropped after three missed heartbeats', async () => {
  const server = await serve((ws, _request, attempt) => {
    if (attempt === 1) ws.send(JSON.stringify({ type: 'welcome', computer_id: 'c-1', heartbeat_interval_ms: 20 }))
    else ws.close(4403)
  })
  const { opts, logs } = options(server.url)
  assert.deepEqual(await runConnection(opts), { kind: 'computer_removed' })
  assert.ok(logs.some((line) => line.includes('no heartbeat from server')))
})

test('relay frames after the welcome reach the relay session', async () => {
  const controller = new AbortController()
  const fromMachine: string[] = []
  const server = await serve((ws) => {
    ws.send(JSON.stringify({ type: 'welcome', computer_id: 'c-1', heartbeat_interval_ms: 1000 }))
    ws.send(JSON.stringify({ type: 'heartbeat_ack' }))
    ws.send(JSON.stringify({ type: 'exec_request', request_id: 'r-1' }))
    ws.send(Buffer.from([1, 2, 3]), { binary: true })
    ws.on('message', (data) => {
      fromMachine.push(data.toString())
      controller.abort()
    })
  })
  const texts: unknown[] = []
  const binaries: Buffer[] = []
  let closed = false
  const { opts } = options(server.url, controller.signal)
  const outcome = await runConnection({
    ...opts,
    relay: (link) => ({
      text: (message) => {
        texts.push(message)
        link.text({ type: 'exec_response', request_id: message.request_id, status: 200 })
      },
      binary: (frame) => binaries.push(frame),
      close: () => { closed = true },
    }),
  })
  assert.deepEqual(outcome, { kind: 'stopped' })
  assert.deepEqual(texts, [{ type: 'exec_request', request_id: 'r-1' }])
  assert.deepEqual(binaries, [Buffer.from([1, 2, 3])])
  assert.deepEqual(fromMachine, ['{"type":"exec_response","request_id":"r-1","status":200}'])
  assert.equal(closed, true)
})

test('the upgrade completing, every frame and a ping count as contact', async () => {
  const controller = new AbortController()
  let heard = 0
  const atFrames: number[] = []
  const server = await serve((ws) => {
    ws.send(JSON.stringify({ type: 'welcome', computer_id: 'c-1', heartbeat_interval_ms: 1000 }))
    ws.send(JSON.stringify({ type: 'heartbeat_ack' }))
    ws.ping()
    ws.send(Buffer.from([1]), { binary: true })
  })
  const { opts } = options(server.url, controller.signal)
  const outcome = runConnection({
    ...opts,
    heard: () => { heard += 1 },
    relay: () => ({
      text: () => {},
      binary: () => {
        atFrames.push(heard)
        controller.abort()
      },
      close: () => {},
    }),
  })
  assert.deepEqual(await outcome, { kind: 'stopped' })
  assert.deepEqual(atFrames, [5], 'open, welcome, heartbeat_ack, ping and the binary frame')
})

test('headers are read again for every attempt', async () => {
  const server = await serve((ws, _request, attempt) => ws.close(attempt === 1 ? 1011 : 4401))
  let version = '1.0.0'
  const { opts } = options(server.url)
  const outcome = await runConnection({
    ...opts,
    headers: () => ({ 'X-Talgent-Claude-Code-Version': version }),
    log: () => { version = '2.0.0' },
  })
  assert.deepEqual(outcome, { kind: 'credential_rejected' })
  assert.deepEqual(server.requests.map((request) => request.headers['x-talgent-claude-code-version']), ['1.0.0', '2.0.0'])
})

test('a rotated credential is saved before the service hears it was, and a failed save says so', async () => {
  for (const outcome of ['saved', 'failed'] as const) {
    const controller = new AbortController()
    const answers: string[] = []
    const server = await serve((ws) => {
      ws.send(JSON.stringify({ type: 'welcome', computer_id: 'c-1' }))
      ws.send(JSON.stringify({ type: 'credential', credential: 'tc_next', expires_at_unix_ms: Date.UTC(2027, 0, 1) }))
      ws.on('message', (data) => {
        const message = JSON.parse(data.toString()) as { type: string }
        if (message.type === 'heartbeat') return
        answers.push(message.type)
        controller.abort()
      })
    })
    const { opts, logs } = options(server.url, controller.signal)
    const issued: unknown[] = []
    const relayed: unknown[] = []
    await runConnection({
      ...opts,
      relay: () => ({ text: (message) => relayed.push(message), binary: () => {}, close: () => {} }),
      credentialIssued: async (credential) => {
        issued.push(credential)
        if (outcome === 'failed') throw new Error('disk full')
      },
    })
    assert.equal(server.requests[0].headers['x-talgent-credential-rotation'], 'supported', 'a run that saves a rotated credential says so')
    assert.deepEqual(issued, [{ credential: 'tc_next', expiresAt: '2027-01-01T00:00:00.000Z' }])
    assert.deepEqual(answers, [outcome === 'saved' ? 'credential_saved' : 'credential_not_saved'])
    assert.deepEqual(relayed, [], 'a credential frame is the connection\'s own')
    assert.ok(logs.every((line) => !line.includes('tc_next')), 'the credential is never logged')
  }
})

test('connect URL follows the server scheme and host', () => {
  assert.equal(connectUrl('https://workbench.example.com'), 'wss://workbench.example.com/computer/v1/connect')
  assert.equal(connectUrl('http://localhost:8080'), 'ws://localhost:8080/computer/v1/connect')
})
