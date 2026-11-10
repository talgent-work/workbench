import assert from 'node:assert/strict'
import { readdir, readFile, mkdtemp, rm, stat } from 'node:fs/promises'
import { request as httpRequest, createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { tmpdir } from 'node:os'
import { join, relative } from 'node:path'
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
import { posixModes } from './support/platform.ts'

const IDENTITY_TOKEN = 'agent-identity-token'

// Talgent Computer black-box tests of a Work's credentials and the local proxy:
// credentials are delivered through the
// execd service as the Gateway relays them, processes run through the shim,
// and the proxy forwards to a stand-in vendor and MCP endpoint.

const MAIN = fileURLToPath(new URL('../src/main.ts', import.meta.url))
const KEY = 'sk-model-key-0123456789'
const SPACE_TOKEN = 'space-token-xyz'

let dir: string
let upstream: Upstream
let proxy: LocalProxy
let processes: ProcessTable
let service: ExecdService
let works: Works
const logs: string[] = []
const created: { workId: string; id: string }[] = []

function newProxy(table: ProcessTable, credentials: CredentialStore): LocalProxy {
  return new LocalProxy({
    portFile: join(dir, 'state', 'proxy.json'),
    access: {
      tokenValid: (workId, token) => table.proxyTokenValid(workId, token),
      credentials: (workId) => credentials.get(workId),
      modelProxy: (workId) => works.modelProxy(workId),
      workRoot: (workId) => works.root(workId),
    },
    log: (message) => logs.push(message),
  })
}

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'proxy-'))
  logs.length = 0
  upstream = new Upstream()
  await upstream.listen()
  processes = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [process.execPath, MAIN, SHIM_ARG] })
  const credentials = new CredentialStore({ dir: join(dir, 'state', 'credentials') })
  works = new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') })
  proxy = newProxy(processes, credentials)
  await proxy.start()
  service = new ExecdService({
    works,
    processes,
    credentials,
    inputs: new WorkInputs({ recordsDir: join(dir, 'state', 'inputs'), credentials, log: () => {} }),
    proxyOrigin: () => proxy.origin(),
    baseEnv: { PATH: '/usr/bin:/bin', ANTHROPIC_API_KEY: 'machine-key' },
    home: '/home/owner',
    log: () => {},
  })
  // w-1 and w-2 run on a Model Configuration, w-login on the machine's own login.
  for (const [workId, modelProxy] of [['w-1', true], ['w-2', true], ['w-login', false]] as const) {
    const body = { access_token: 'tok', ...(modelProxy ? { model_proxy: true } : {}) }
    assert.equal((await service.request({ workId, method: 'PUT', path: '/', token: '', body })).status, 200)
  }
})
afterEach(async () => {
  for (const { workId, id } of created.splice(0)) await processes.delete(workId, id)
  await proxy.close()
  await upstream.close()
  await rm(dir, { recursive: true, force: true })
})

const credentialsBody = (model = true) => ({
  ...(model ? { model: { base_url: `${upstream.origin}/anthropic`, key: KEY } } : {}),
  gateway: { url: `${upstream.origin}/computer/v1/mcp`, identity_token: IDENTITY_TOKEN, space_token: SPACE_TOKEN },
})

const deliver = (body: unknown, workId = 'w-1', method = 'PUT') => service.request({ workId, method, path: '/credentials', token: '', body })

// Starts command as a process of workId and answers once its stdout holds
// marker; the process keeps running until it ends or the test deletes it.
async function start(command: string, marker: string, workId = 'w-1'): Promise<{ id: string; stdout: string; ended: () => boolean }> {
  const response = await service.request({ workId, method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', command } })
  assert.equal(response.status, 201)
  const id = (response.body as { session_id: string }).session_id
  created.push({ workId, id })
  const binaries: Buffer[] = []
  let ended = false
  const sink: PipeSink = {
    closed: new AbortController().signal,
    text: () => {},
    binary: (frame) => binaries.push(frame),
    end: () => { ended = true },
    writable: async () => {},
  }
  const pipe = await service.openStream({ workId, processId: id, since: 0, token: 'tok' }, sink)
  assert.ok(!('status' in pipe))
  const stdout = () => binaries.filter((frame) => frame[0] === 1).map((frame) => frame.subarray(1).toString()).join('')
  const deadline = Date.now() + 10_000
  while (!stdout().includes(marker)) {
    if (Date.now() > deadline) throw new Error(`no ${marker} in ${stdout()}`)
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  return { id, stdout: stdout(), ended: () => ended }
}

// A running process of workId and the environment it sees.
async function runEnv(workId = 'w-1', then = 'exec sleep 600'): Promise<{ id: string; env: Record<string, string>; ended: () => boolean }> {
  const { id, stdout, ended } = await start(`env; echo __END__; ${then}`, '__END__', workId)
  const env: Record<string, string> = {}
  for (const line of stdout.split('\n')) {
    const at = line.indexOf('=')
    if (at > 0) env[line.slice(0, at)] = line.slice(at + 1)
  }
  return { id, env, ended }
}

async function filesUnder(root: string): Promise<string[]> {
  const files: string[] = []
  for (const entry of await readdir(root, { withFileTypes: true, recursive: true })) {
    if (entry.isFile()) files.push(join(entry.parentPath, entry.name))
  }
  return files
}

test('credentials are validated, kept 0600 outside every Work root, and DELETE forgets them', async () => {
  assert.deepEqual(await deliver(credentialsBody()), { status: 204 })
  const file = join(dir, 'state', 'credentials', 'w-1.json')
  if (posixModes) {
    assert.equal((await stat(file)).mode & 0o777, 0o600)
    assert.equal((await stat(join(dir, 'state', 'credentials'))).mode & 0o777, 0o700)
  }
  assert.ok(relative(join(dir, 'works'), file).startsWith('..'), 'outside every Work root')
  assert.equal((await deliver(credentialsBody(), 'w-unknown')).status, 404)
  assert.equal((await deliver(undefined, 'w-1', 'GET')).status, 405)
  const gateway = credentialsBody().gateway
  for (const body of [
    null,
    {},
    { gateway: { ...gateway, url: 'ftp://x/mcp' } },
    { gateway: { ...gateway, identity_token: '' } },
    { gateway: { ...gateway, identity_token: 'a b' } },
    { gateway, model: { base_url: 'http://vendor.example/anthropic', key: KEY } },
    { gateway, model: { base_url: 'https://vendor.example/a?x=1', key: KEY } },
    { gateway, model: { base_url: 'https://u:p@vendor.example/a', key: KEY } },
    { gateway, model: { base_url: 'https://vendor.example/a', key: '' } },
    { gateway, model: 'x' },
    { gateway, api: { url: 'https://other.example' } },
  ]) {
    const answer = await deliver(body)
    assert.equal(answer.status, 400, JSON.stringify(body))
    assert.equal((answer.body as { code: string }).code, 'INVALID_REQUEST_BODY')
    assert.ok(!JSON.stringify(answer.body).includes(KEY), 'the error names no value')
  }
  assert.deepEqual(await deliver(undefined, 'w-1', 'DELETE'), { status: 204 })
  await assert.rejects(stat(file), { code: 'ENOENT' })
})

test('a run reaches the model only through the proxy; neither its environment nor any file holds the key or Identity Token', async () => {
  await deliver(credentialsBody())
  const { id, env } = await runEnv()
  const token = env.TALGENT_PROXY_TOKEN
  assert.match(token, /^[A-Za-z0-9_-]{43}$/)
  assert.equal(env.TALGENT_PROXY_URL, `${proxy.origin()}/w/w-1`)
  assert.equal(env.ANTHROPIC_BASE_URL, `${proxy.origin()}/w/w-1/model`)
  assert.equal(env.ANTHROPIC_AUTH_TOKEN, token)
  assert.equal(env.ANTHROPIC_API_KEY, undefined, 'the machine key is removed')
  const spawned = JSON.stringify(env)
  for (const secret of [KEY, IDENTITY_TOKEN, SPACE_TOKEN]) assert.ok(!spawned.includes(secret))
  // Every file Talgent Computer keeps, the Work root and process state
  // included, but the credentials file itself.
  const credentialsFile = join(dir, 'state', 'credentials', 'w-1.json')
  for (const file of await filesUnder(dir)) {
    if (file === credentialsFile) continue
    const text = await readFile(file, 'utf8').catch(() => '')
    for (const secret of [KEY, IDENTITY_TOKEN, SPACE_TOKEN, token]) assert.ok(!text.includes(secret) || (secret === token && file.endsWith('output.log')), `${file} holds a secret`)
  }
  const record = await readFile(join(processes.dir('w-1', id), 'proxy-token.json'), 'utf8')
  assert.ok(!record.includes(token), 'only the token hash is kept')
})

test('a Work the platform serves the entry to calls it through the proxy with its run token', async () => {
  assert.equal((await service.request({ workId: 'w-api', method: 'PUT', path: '/', token: '', body: { access_token: 'tok', platform_api: true } })).status, 200)
  const { env } = await runEnv('w-api')
  assert.equal(env.TALGENT_API_ADDRESS, `${proxy.origin()}/w/w-api/api/${env.TALGENT_PROXY_TOKEN}`)
  assert.equal((await runEnv('w-1')).env.TALGENT_API_ADDRESS, undefined, 'a Work without it reaches the platform otherwise')
})

test("a run's telemetry goes to the platform with its Work's token, never where the machine points its own", async () => {
  const telemetry = { endpoint: 'https://workbench.example/computer/v1/otel', token: 'TELEMETRYTOKEN234567' }
  const opened = await service.request({ workId: 'w-1', method: 'PUT', path: '/', token: '', body: { access_token: 'tok', model_proxy: true, environment: { CLAUDE_CODE_ENABLE_TELEMETRY: '1' }, telemetry } })
  assert.equal(opened.status, 200)
  service = new ExecdService({
    works,
    processes,
    credentials: new CredentialStore({ dir: join(dir, 'state', 'credentials') }),
    inputs: new WorkInputs({ recordsDir: join(dir, 'state', 'inputs'), credentials: new CredentialStore({ dir: join(dir, 'state', 'credentials') }), log: () => {} }),
    proxyOrigin: () => proxy.origin(),
    baseEnv: { PATH: '/usr/bin:/bin', OTEL_EXPORTER_OTLP_ENDPOINT: 'https://mine.example', OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: 'https://mine.example/v1/traces', OTEL_EXPORTER_OTLP_LOGS_HEADERS: 'Authorization=Bearer mine' },
    home: '/home/owner',
    log: () => {},
  })
  const { env } = await runEnv()
  assert.equal(env.CLAUDE_CODE_ENABLE_TELEMETRY, '1')
  assert.equal(env.OTEL_EXPORTER_OTLP_ENDPOINT, telemetry.endpoint)
  assert.equal(env.OTEL_EXPORTER_OTLP_HEADERS, `Authorization=Bearer ${telemetry.token},X-Talgent-Work-Id=w-1`)
  assert.equal(env.OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, undefined, "the machine's own exporter settings are removed")
  assert.equal(env.OTEL_EXPORTER_OTLP_LOGS_HEADERS, undefined)
  const { env: other } = await runEnv('w-2')
  assert.equal(other.OTEL_EXPORTER_OTLP_ENDPOINT, 'https://mine.example', 'a Work the platform named no telemetry for keeps the machine environment')
})

test('a Work on the machine login gets the proxy but keeps its own Anthropic settings and no model route', async () => {
  await deliver(credentialsBody(), 'w-login')
  const { env } = await runEnv('w-login')
  assert.equal(env.TALGENT_PROXY_URL, `${proxy.origin()}/w/w-login`)
  assert.ok(env.TALGENT_PROXY_TOKEN.length > 0)
  assert.equal(env.ANTHROPIC_BASE_URL, undefined)
  assert.equal(env.ANTHROPIC_AUTH_TOKEN, undefined)
  assert.equal(env.ANTHROPIC_API_KEY, 'machine-key')
  const auth = { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}` }
  const model = await send(`${proxy.origin()}/w/w-login/model/v1/messages`, { method: 'POST', headers: auth, body: '{}' })
  assert.equal(model.status, 404, 'even though the credentials hold a model')
  assert.equal(upstream.seen.length, 0)
  assert.equal((await send(`${proxy.origin()}/w/w-login/mcp`, { method: 'POST', headers: auth, body: '{}' })).status, 200)
})

test('a Model Configuration Work run before delivery reaches only the proxy, which waits for the key', async () => {
  const { env } = await runEnv()
  assert.equal(env.ANTHROPIC_BASE_URL, `${proxy.origin()}/w/w-1/model`)
  assert.equal(env.ANTHROPIC_AUTH_TOKEN, env.TALGENT_PROXY_TOKEN)
  assert.equal(env.ANTHROPIC_API_KEY, undefined, 'never the machine login')
  const call = () => send(`${env.ANTHROPIC_BASE_URL}/v1/messages`, { method: 'POST', headers: { authorization: `Bearer ${env.ANTHROPIC_AUTH_TOKEN}` }, body: '{}' })
  const undelivered = await call()
  assert.equal(undelivered.status, 503)
  assert.equal(JSON.parse(undelivered.body).code, 'CREDENTIALS_NOT_DELIVERED')
  await deliver(credentialsBody(false))
  assert.equal((await call()).status, 503, 'credentials without a model')
  await deliver(credentialsBody())
  assert.equal((await call()).status, 200, 'the same run, once delivered')
  assert.equal(upstream.last().headers.authorization, `Bearer ${KEY}`)
  await deliver(undefined, 'w-1', 'DELETE')
  assert.equal((await call()).status, 503, 'withdrawn')
})

test('the key is injected only on POST of the two messages paths; the client credentials never pass', async () => {
  await deliver(credentialsBody())
  const { env } = await runEnv()
  const token = env.TALGENT_PROXY_TOKEN
  const base = `${proxy.origin()}/w/w-1/model`
  const clientHeaders = {
    authorization: `Bearer ${token}`,
    'x-api-key': 'client-key',
    cookie: 'session=1',
    'proxy-authorization': 'Basic x',
    'x-talgent-work-credential': 'forged',
    'anthropic-version': '2023-06-01',
    'content-type': 'application/json',
  }
  for (const path of ['/v1/messages', '/v1/messages/count_tokens']) {
    const answer = await send(`${base}${path}?beta=true`, { method: 'POST', headers: clientHeaders, body: '{"model":"m"}' })
    assert.equal(answer.status, 200)
    assert.equal(answer.headers['x-upstream'], 'yes')
    assert.equal(answer.headers['set-cookie'], undefined, 'upstream cookies are not relayed')
    const seen = upstream.last()
    assert.equal(seen.url, `/anthropic${path}?beta=true`, 'the path goes under the base path, query kept')
    assert.equal(seen.headers.authorization, `Bearer ${KEY}`)
    assert.equal(seen.headers['anthropic-version'], '2023-06-01')
    assert.equal(seen.body, '{"model":"m"}')
    for (const name of ['x-api-key', 'cookie', 'proxy-authorization', 'x-talgent-work-credential']) assert.equal(seen.headers[name], undefined, name)
  }
  for (const [method, path] of [['GET', '/v1/messages'], ['GET', '/v1/models'], ['POST', '/v1/complete'], ['POST', '/v1/messages/batches']]) {
    const answer = await send(`${base}${path}`, { method, headers: { 'x-api-key': token } })
    assert.equal(answer.status, 200, `${method} ${path}`)
    assert.equal(upstream.last().headers.authorization, undefined, `${method} ${path} carries no key`)
    assert.equal(upstream.last().headers['x-api-key'], undefined)
  }
  for (const rawPath of ['/w/w-1/model/v1/../v1/messages', '/w/w-1/model/x/%2E%2e/v1/messages']) {
    assert.equal((await send(base, { rawPath, method: 'POST', headers: { 'x-api-key': token } })).status, 404, rawPath)
  }
})

test('a streamed model response reaches the process as it arrives', async () => {
  await deliver(credentialsBody())
  const { env } = await runEnv()
  upstream.hold()
  const first = await new Promise<{ chunk: string; rest: Promise<string> }>((resolve, reject) => {
    const outgoing = httpRequest(`${proxy.origin()}/w/w-1/model/v1/messages?stream=true`, {
      method: 'POST',
      headers: { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}`, 'content-type': 'application/json' },
    }, (response) => {
      assert.equal(response.headers['content-type'], 'text/event-stream')
      response.once('data', (chunk: Buffer) => {
        const rest = new Promise<string>((done) => {
          let text = ''
          response.on('data', (more: Buffer) => { text += more.toString() })
          response.on('end', () => done(text))
        })
        resolve({ chunk: chunk.toString(), rest })
      })
    })
    outgoing.on('error', reject)
    outgoing.end('{"stream":true}')
  })
  assert.equal(first.chunk, 'event: message_start\ndata: {}\n\n', 'the first event arrives while the upstream still holds the stream')
  upstream.release()
  assert.equal(await first.rest, 'event: message_stop\ndata: {}\n\n')
})

test('MCP requests go to the Talgent MCP endpoint with the Work\'s Identity Token and Space Token alone', async () => {
  await deliver(credentialsBody())
  const { env } = await runEnv()
  const headers = { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}`, 'x-talgent-work-id': 'forged', 'content-type': 'application/json' }
  for (const [path, expected] of [['/mcp', '/computer/v1/mcp'], ['/mcp/sub?x=1', '/computer/v1/mcp/sub?x=1']]) {
    assert.equal((await send(`${proxy.origin()}/w/w-1${path}`, { method: 'POST', headers, body: '{"jsonrpc":"2.0"}' })).status, 200)
    const seen = upstream.last()
    assert.equal(seen.url, expected)
    assert.equal(seen.headers['x-talgent-work-id'], undefined, 'a client identity header never passes')
    assert.equal(seen.headers['x-talgent-work-credential'], undefined)
    assert.equal(seen.headers['x-talgent-space-token'], SPACE_TOKEN)
    assert.equal(seen.headers.authorization, `Bearer ${IDENTITY_TOKEN}`, 'the run\'s token is replaced by the Work\'s Identity Token')
  }
  const { space_token: _dropped, ...withoutSpace } = credentialsBody().gateway
  void _dropped
  await deliver({ gateway: withoutSpace })
  await send(`${proxy.origin()}/w/w-1/mcp`, { method: 'POST', headers, body: '{}' })
  assert.equal(upstream.last().headers['x-talgent-space-token'], undefined)
})

test('only the token of a running run of the Work is accepted', async () => {
  await deliver(credentialsBody())
  await deliver(credentialsBody(), 'w-2')
  const { id, env } = await runEnv()
  const token = env.TALGENT_PROXY_TOKEN
  const mcp = (workId: string, headers: Record<string, string>) => send(`${proxy.origin()}/w/${workId}/mcp`, { method: 'POST', headers, body: '{}' })
  assert.equal((await mcp('w-1', {})).status, 401)
  assert.equal((await mcp('w-1', { authorization: 'Bearer wrong' })).status, 401)
  assert.equal((await mcp('w-2', { authorization: `Bearer ${token}` })).status, 401, 'another Work')
  assert.equal((await mcp('w-1', { 'x-api-key': token })).status, 200)
  assert.equal(upstream.seen.length, 1, 'refused requests reach nothing')

  // A Talgent Computer restart reads the token back from the state dir.
  const restarted = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [] })
  assert.equal(await restarted.proxyTokenValid('w-1', token), true)
  assert.equal(await restarted.proxyTokenValid('w-2', token), false)

  assert.equal((await service.request({ workId: 'w-1', method: 'DELETE', path: `/pty/${id}`, token: 'tok' })).status, 200)
  created.splice(0)
  assert.equal((await mcp('w-1', { authorization: `Bearer ${token}` })).status, 401, 'a deleted process')
  assert.equal(await restarted.proxyTokenValid('w-1', token), false)
})

test('a run token ends with its run', async () => {
  await deliver(credentialsBody())
  const { id, env, ended } = await runEnv('w-1', 'read line')
  const token = env.TALGENT_PROXY_TOKEN
  const mcp = () => send(`${proxy.origin()}/w/w-1/mcp`, { method: 'POST', headers: { authorization: `Bearer ${token}` }, body: '{}' })
  assert.equal((await mcp()).status, 200)
  await processes.signal('w-1', id, 'SIGKILL')
  const deadline = Date.now() + 10_000
  while (!ended()) {
    if (Date.now() > deadline) throw new Error('the run did not end')
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  assert.equal((await processes.phase('w-1', id))?.kind, 'exited')
  assert.equal((await mcp()).status, 401, 'the exited run')
})

test('a path cannot leave the base path of either upstream', async () => {
  await deliver(credentialsBody())
  const { env } = await runEnv()
  const headers = { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}` }
  const escapes = ['/a\\..\\..\\..\\api\\x', '/..\\x', '/../x', '/./x', '/a/%2e%2e/x', '/%2E%2E/x', '/a%2f..%2fx', '/a%2F..', '/a%5c..%5cx', '/a%5C..', '/%2e/x']
  for (const route of ['model', 'mcp']) {
    for (const escape of escapes) {
      const rawPath = `/w/w-1/${route}${escape}`
      const answer = await send(proxy.origin(), { rawPath, method: 'POST', headers, body: '{}' })
      assert.equal(answer.status, 404, rawPath)
    }
  }
  assert.equal(upstream.seen.length, 0, 'nothing reached the upstream')
})

test('undelivered credentials answer 503, unknown routes 404 and an unreachable upstream 502 without secrets', async () => {
  const { env } = await runEnv()
  const auth = { authorization: `Bearer ${env.TALGENT_PROXY_TOKEN}` }
  const undelivered = await send(`${proxy.origin()}/w/w-1/model/v1/messages`, { method: 'POST', headers: auth, body: '{}' })
  assert.equal(undelivered.status, 503)
  assert.equal(JSON.parse(undelivered.body).code, 'CREDENTIALS_NOT_DELIVERED')
  await deliver(credentialsBody())
  await deliver(undefined, 'w-1', 'DELETE')
  assert.equal((await send(`${proxy.origin()}/w/w-1/mcp`, { method: 'POST', headers: auth })).status, 503, 'forgotten')
  for (const rawPath of ['/', '/w/w-1', '/w/w-1/other', '/w/../mcp', '/w/a.b/mcp', '/x/w-1/mcp']) {
    assert.equal((await send(proxy.origin(), { rawPath, headers: auth })).status, 404, rawPath)
  }
  const closed = createServer()
  await new Promise<void>((resolve) => closed.listen(0, '127.0.0.1', resolve))
  const port = (closed.address() as AddressInfo).port
  await new Promise<void>((resolve) => closed.close(() => resolve()))
  await deliver({ model: { base_url: `http://127.0.0.1:${port}/anthropic`, key: KEY }, gateway: credentialsBody().gateway })
  const failed = await send(`${proxy.origin()}/w/w-1/model/v1/messages`, { method: 'POST', headers: auth, body: '{}' })
  assert.equal(failed.status, 502)
  assert.ok(!failed.body.includes(KEY))
  assert.ok(logs.some((line) => line.includes('ECONNREFUSED')))
  assert.ok(logs.every((line) => !line.includes(KEY) && !line.includes(IDENTITY_TOKEN)))
})

test('the proxy keeps its port across restarts and moves when it is taken', async () => {
  const port = new URL(proxy.origin()).port
  await proxy.close()
  const credentials = new CredentialStore({ dir: join(dir, 'state', 'credentials') })
  proxy = newProxy(processes, credentials)
  await proxy.start()
  assert.equal(new URL(proxy.origin()).port, port, 'the persisted port is reused')
  await proxy.close()

  const squatter = createServer()
  await new Promise<void>((resolve) => squatter.listen(Number(port), '127.0.0.1', resolve))
  try {
    proxy = newProxy(processes, credentials)
    await proxy.start()
    const moved = new URL(proxy.origin()).port
    assert.notEqual(moved, port)
    assert.ok(logs.some((line) => line.includes(`port ${port} is taken`)))
    assert.deepEqual(JSON.parse(await readFile(join(dir, 'state', 'proxy.json'), 'utf8')), { port: Number(moved) })
  } finally {
    await new Promise<void>((resolve) => squatter.close(() => resolve()))
  }
})
