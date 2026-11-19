import assert from 'node:assert/strict'
import { mkdtemp, readdir, readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, test } from 'node:test'
import type { PipeSink } from '../src/execd/pipe.ts'
import { WorkInputs } from '../src/execd/inputs.ts'
import { ProcessTable } from '../src/execd/processes.ts'
import { ExecdService } from '../src/execd/service.ts'
import { SHIM_ARG } from '../src/execd/shim.ts'
import { Works } from '../src/execd/works.ts'
import { CredentialStore } from '../src/proxy/credentials.ts'
import { LocalProxy } from '../src/proxy/server.ts'
import { send, Upstream } from './support/upstream.ts'
import { removeScratch } from './support/platform.ts'

// Talgent Computer black-box tests of a Work's Provider Connectors: the
// platform delivers each one's short-lived target credential through the
// execd service, as the Gateway relays it, and a run's process reaches the
// Connector at its own explicit proxy address, which forwards to the
// Connector's address with that credential added.

const MAIN = fileURLToPath(new URL('../src/main.ts', import.meta.url))
const IDENTITY_TOKEN = 'agent-identity-token'
const TARGET_TOKEN = 'target-access-token-1'
const OTHER_TOKEN = 'other-target-token'
const CONNECTOR = '01900000-0000-7000-8000-000000000390'
const OTHER = '01900000-0000-7000-8000-000000000391'

let dir: string
let upstream: Upstream
let proxy: LocalProxy
let processes: ProcessTable
let service: ExecdService
const created: string[] = []

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'connector-proxy-'))
  upstream = new Upstream()
  await upstream.listen()
  processes = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [process.execPath, MAIN, SHIM_ARG] })
  const credentials = new CredentialStore({ dir: join(dir, 'state', 'credentials') })
  const works = new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') })
  proxy = new LocalProxy({
    portFile: join(dir, 'state', 'proxy.json'),
    access: {
      tokenValid: (workId, token) => processes.proxyTokenValid(workId, token),
      credentials: (workId) => credentials.get(workId),
      modelProxy: (workId) => works.modelProxy(workId),
      workRoot: (workId) => works.root(workId),
    },
    log: () => {},
  })
  await proxy.start()
  service = new ExecdService({
    works,
    processes,
    credentials,
    inputs: new WorkInputs({ recordsDir: join(dir, 'state', 'inputs'), credentials, log: () => {} }),
    proxyOrigin: () => proxy.origin(),
    baseEnv: { PATH: '/usr/bin:/bin' },
    home: '/home/owner',
    log: () => {},
  })
  assert.equal((await service.request({ workId: 'w-1', method: 'PUT', path: '/', token: '', body: { access_token: 'tok' } })).status, 200)
})
afterEach(async () => {
  for (const id of created.splice(0)) await processes.delete('w-1', id)
  await proxy.close()
  await upstream.close()
  await removeScratch(dir)
})

const credentialsBody = (connectors?: unknown) => ({
  gateway: { url: `${upstream.origin}/computer/v1/mcp`, identity_token: IDENTITY_TOKEN },
  ...(connectors === undefined ? {} : { connectors }),
})
const delivered = (token = TARGET_TOKEN) => [
  { id: CONNECTOR, url: `${upstream.origin}/enterprise/mcp/`, token },
  { id: OTHER, url: `${upstream.origin}/other/mcp`, token: OTHER_TOKEN },
]
const deliver = (body: unknown) => service.request({ workId: 'w-1', method: 'PUT', path: '/credentials', token: '', body })

// A running process of the Work and the environment it sees.
async function runEnv(): Promise<Record<string, string>> {
  const response = await service.request({ workId: 'w-1', method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', command: 'env; echo __END__; exec sleep 600' } })
  assert.equal(response.status, 201)
  const id = (response.body as { session_id: string }).session_id
  created.push(id)
  const frames: Buffer[] = []
  const sink: PipeSink = { closed: new AbortController().signal, text: () => {}, binary: (frame) => frames.push(frame), end: () => {}, writable: async () => {} }
  assert.ok(!('status' in await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, sink)))
  const stdout = () => frames.filter((frame) => frame[0] === 1).map((frame) => frame.subarray(1).toString()).join('')
  const deadline = Date.now() + 2_000
  while (!stdout().includes('__END__')) {
    if (Date.now() > deadline) throw new Error('the process printed no environment')
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  return Object.fromEntries(stdout().split('\n').filter((line) => line.indexOf('=') > 0).map((line) => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]))
}

test('a Provider Connector\'s proxy address forwards to its own address with its target credential alone', async () => {
  assert.deepEqual(await deliver(credentialsBody(delivered())), { status: 204 })
  const env = await runEnv()
  const address = `${env.TALGENT_PROXY_URL}/connectors/${CONNECTOR}`
  const run = { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}` }

  const answer = await send(address, { method: 'POST', headers: { ...run, 'content-type': 'application/json', 'mcp-session-id': 's-1', 'x-talgent-space-token': 'forged', cookie: 'c=1' }, body: '{"jsonrpc":"2.0","id":1,"method":"initialize"}' })
  assert.equal(answer.status, 200)
  const seen = upstream.last()
  assert.equal(seen.url, '/enterprise/mcp/', 'the declared address, trailing slash included')
  assert.equal(seen.headers.authorization, `Bearer ${TARGET_TOKEN}`)
  assert.equal(seen.headers['mcp-session-id'], 's-1', 'the MCP session travels')
  assert.equal(seen.body, '{"jsonrpc":"2.0","id":1,"method":"initialize"}')
  for (const name of ['x-talgent-space-token', 'cookie']) assert.equal(seen.headers[name], undefined, `${name} is not forwarded`)
  assert.equal(answer.headers['set-cookie'], undefined)

  await send(`${address}/sse?cursor=2`, { headers: run })
  assert.equal(upstream.last().url, '/enterprise/mcp/sse?cursor=2', 'a path below the address and its query are kept')
  await send(`${env.TALGENT_PROXY_URL}/connectors/${OTHER}`, { headers: run })
  assert.equal(upstream.last().headers.authorization, `Bearer ${OTHER_TOKEN}`, 'each Connector takes its own credential')

  const reached = upstream.seen.length
  for (const rawPath of [`/w/w-1/connectors/${CONNECTOR}/../../other/mcp`, `/w/w-1/connectors/${CONNECTOR}/%2e%2e/x`]) {
    assert.equal((await send(proxy.origin(), { rawPath, headers: run })).status, 404, rawPath)
  }
  assert.equal(upstream.seen.length, reached, 'nothing escapes the Connector\'s address')

  const spawned = JSON.stringify(env)
  for (const secret of [TARGET_TOKEN, OTHER_TOKEN, IDENTITY_TOKEN]) assert.ok(!spawned.includes(secret), 'no credential is in the process environment')
  for (const entry of await readdir(dir, { recursive: true, withFileTypes: true })) {
    if (!entry.isFile() || entry.name === 'w-1.json') continue
    const text = await readFile(join(entry.parentPath, entry.name), 'utf8').catch(() => '')
    assert.ok(!text.includes(TARGET_TOKEN), `${entry.name} holds the target credential`)
  }
})

test('a Connector address takes only a run token of its Work, and an undelivered Connector is unavailable alone', async () => {
  await deliver(credentialsBody([delivered()[1]]))
  const env = await runEnv()
  const run = { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}` }
  const address = `${env.TALGENT_PROXY_URL}/connectors/${CONNECTOR}`
  assert.equal((await send(address, { headers: { authorization: 'Bearer forged' } })).status, 401)
  const unavailable = await send(address, { headers: run })
  assert.equal(unavailable.status, 503)
  assert.equal(JSON.parse(unavailable.body).code, 'CONNECTOR_UNAVAILABLE')
  assert.equal((await send(`${env.TALGENT_PROXY_URL}/connectors/${OTHER}`, { headers: run })).status, 200, 'the Work\'s other Connector works')
  assert.equal((await send(`${env.TALGENT_PROXY_URL}/mcp`, { method: 'POST', headers: run, body: '{}' })).status, 200, 'and so does the Talgent Connector')
})

test('a redelivery replaces a Connector\'s target credential, and one without it leaves the Connector unavailable', async () => {
  await deliver(credentialsBody(delivered()))
  const env = await runEnv()
  const run = { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}` }
  const address = `${env.TALGENT_PROXY_URL}/connectors/${CONNECTOR}`
  assert.deepEqual(await deliver(credentialsBody(delivered('target-access-token-2'))), { status: 204 })
  await send(address, { headers: run })
  assert.equal(upstream.last().headers.authorization, 'Bearer target-access-token-2', 'the refreshed credential is added at once')
  await deliver(credentialsBody())
  assert.equal((await send(address, { headers: run })).status, 503)
})

test('a delivery with an invalid Connector is refused without naming its credential', async () => {
  const connector = delivered()[0]
  for (const connectors of [
    'x',
    [null],
    [{ ...connector, id: '' }],
    [{ ...connector, id: 'a/b' }],
    [connector, { ...connector, token: OTHER_TOKEN }],
    [{ ...connector, url: 'http://enterprise.example/mcp' }],
    [{ ...connector, url: 'https://enterprise.example/mcp?key=1' }],
    [{ ...connector, url: 'https://u:p@enterprise.example/mcp' }],
    [{ ...connector, token: '' }],
    [{ ...connector, token: 'a b' }],
  ]) {
    const answer = await deliver(credentialsBody(connectors))
    assert.equal(answer.status, 400, JSON.stringify(connectors))
    assert.ok(!JSON.stringify(answer.body).includes(TARGET_TOKEN), 'the error names no value')
  }
})
