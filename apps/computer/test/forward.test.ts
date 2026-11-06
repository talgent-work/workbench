import assert from 'node:assert/strict'
import { createServer, request as httpRequest, type Server } from 'node:http'
import { createServer as createTcpServer, type AddressInfo, type Server as TcpServer, type Socket } from 'node:net'
import { afterEach, test } from 'node:test'
import { forward } from '../src/proxy/forward.ts'

// forward()'s failure paths, with short timeouts: an upstream that never
// completes the connection, one that never answers, and one that stalls a
// stream it started.

const timeouts = { connectMs: 200, idleMs: 300 }
const servers: (Server | TcpServer)[] = []
const sockets: Socket[] = []
afterEach(async () => {
  for (const socket of sockets.splice(0)) socket.destroy()
  for (const server of servers.splice(0)) {
    if ('closeAllConnections' in server) server.closeAllConnections()
    await new Promise<void>((resolve) => server.close(() => resolve()))
  }
})

async function listen<T extends Server | TcpServer>(server: T): Promise<number> {
  servers.push(server)
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  return (server.address() as AddressInfo).port
}

// A proxy in front of target that records why forwarding failed.
async function proxyTo(target: string): Promise<{ port: number; failures: string[] }> {
  const failures: string[] = []
  const port = await listen(createServer((request, response) => {
    forward(request, response, new URL(target), {}, { timeouts, fail: (reason) => failures.push(reason) })
  }))
  return { port, failures }
}

function get(port: number): Promise<{ status: number; body: string; aborted: boolean }> {
  return new Promise((resolve, reject) => {
    const outgoing = httpRequest({ host: '127.0.0.1', port, path: '/' }, (response) => {
      let body = ''
      let aborted = false
      response.on('data', (chunk: Buffer) => { body += chunk.toString() })
      response.on('aborted', () => { aborted = true })
      response.on('error', () => { aborted = true })
      response.on('close', () => resolve({ status: response.statusCode ?? 0, body, aborted: aborted || !response.complete }))
    })
    outgoing.on('error', reject)
    outgoing.end()
  })
}

test('an upstream that never completes the connection answers 502 after the connect timeout', async () => {
  // TCP accepts, but the TLS handshake never answers.
  const silent = await listen(createTcpServer((socket) => sockets.push(socket)))
  const { port, failures } = await proxyTo(`https://127.0.0.1:${silent}/`)
  const started = Date.now()
  const answer = await get(port)
  assert.equal(answer.status, 502)
  assert.deepEqual(failures, ['connect timeout'])
  assert.ok(Date.now() - started < timeouts.idleMs + 1000)
})

test('an upstream that never answers answers 502 after the idle timeout', async () => {
  const mute = await listen(createServer(() => {}))
  const { port, failures } = await proxyTo(`http://127.0.0.1:${mute}/`)
  const answer = await get(port)
  assert.equal(answer.status, 502)
  assert.equal(JSON.parse(answer.body).code, 'UPSTREAM_UNAVAILABLE')
  assert.deepEqual(failures, ['idle timeout'])
})

test('a stream that stalls longer than the idle timeout is cut; one that keeps talking is not', async () => {
  const upstream = await listen(createServer((request, response) => {
    response.writeHead(200, { 'Content-Type': 'text/event-stream' })
    if (request.url === '/stall') {
      response.write('data: 1\n\n')
      return
    }
    // Quiet less than the idle timeout each time, longer than it in all.
    let sent = 0
    const timer = setInterval(() => {
      sent += 1
      if (sent < 4) response.write(`data: ${sent}\n\n`)
      else {
        clearInterval(timer)
        response.end()
      }
    }, timeouts.idleMs / 2)
  }))
  const stalled = await proxyTo(`http://127.0.0.1:${upstream}/stall`)
  const cut = await get(stalled.port)
  assert.equal(cut.status, 200)
  assert.equal(cut.body, 'data: 1\n\n')
  assert.equal(cut.aborted, true)
  assert.deepEqual(stalled.failures, ['idle timeout'])

  const talking = await proxyTo(`http://127.0.0.1:${upstream}/talk`)
  const whole = await get(talking.port)
  assert.equal(whole.aborted, false)
  assert.equal(whole.body, 'data: 1\n\ndata: 2\n\ndata: 3\n\n')
  assert.deepEqual(talking.failures, [])
})
