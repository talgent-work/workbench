import assert from 'node:assert/strict'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import type { WorkCredentials } from '../src/proxy/credentials.ts'
import { LocalProxy } from '../src/proxy/server.ts'
import { send, Upstream } from './support/upstream.ts'

// The native API route: a Work's plain HTTP client, such as curl, calls the
// platform's native API through the local proxy with the run's token in the
// path. The proxy adds the Work's identity, and for an Artifact command
// naming a file of the Work, sends the file's bytes as content instead.

const TOKEN = 'run-token'
const CREATE = '/storage.private.v1.ArtifactService/CreateArtifact'

let dir: string
let upstream: Upstream
let proxy: LocalProxy
let credentials: WorkCredentials | undefined

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'api-route-'))
  const root = join(dir, 'works', 'w-1')
  await mkdir(join(root, 'dist'), { recursive: true })
  await mkdir(join(root, 'inputs'), { recursive: true })
  await writeFile(join(root, 'dist', 'report.md'), '# Report\n')
  await writeFile(join(root, 'inputs', 'brief.md'), '# Brief\n')
  upstream = new Upstream()
  await upstream.listen()
  credentials = {
    gateway: { url: `${upstream.origin}/computer/v1/mcp`, workId: 'w-1', workCredential: 'cred', spaceToken: 'space' },
    api: { url: `${upstream.origin}/computer/v1/api` },
  }
  proxy = new LocalProxy({
    portFile: join(dir, 'state', 'proxy.json'),
    access: {
      tokenValid: async (workId, token) => workId === 'w-1' && token === TOKEN,
      credentials: async () => credentials,
      modelProxy: async () => false,
      workRoot: (workId) => join(dir, 'works', workId),
    },
    log: () => {},
  })
  await proxy.start()
})
afterEach(async () => {
  await proxy.close()
  await upstream.close()
  await rm(dir, { recursive: true, force: true })
})

const api = (path: string, body?: string, token = TOKEN, headers: Record<string, string> = {}) =>
  send(`${proxy.origin()}/w/w-1/api/${token}${path}`, { method: body === undefined ? 'GET' : 'POST', headers: { 'content-type': 'application/json', ...headers }, body })

test('a call reaches the native API with the Work identity, never one the process adds', async () => {
  const answer = await api('/agent.public.v1.PersonalWorkService/ListLibraryFiles', '{"nameQuery":"report"}', TOKEN, { 'x-talgent-work-id': 'forged', authorization: 'Bearer x' })
  assert.equal(answer.status, 200)
  const seen = upstream.last()
  assert.equal(seen.url, '/computer/v1/api/agent.public.v1.PersonalWorkService/ListLibraryFiles')
  assert.equal(seen.headers['x-talgent-work-id'], 'w-1')
  assert.equal(seen.headers['x-talgent-work-credential'], 'cred')
  assert.equal(seen.headers['x-talgent-space-token'], 'space')
  assert.equal(seen.headers.authorization, undefined)
  assert.equal(seen.body, '{"nameQuery":"report"}')

  // An Access Link the Work was issued downloads the same way.
  assert.equal((await api('/v1/storage/content?grant=signed')).status, 200)
  assert.equal(upstream.last().url, '/computer/v1/api/v1/storage/content?grant=signed')
})

test('an Artifact command naming a file of the Work sends its bytes instead', async () => {
  assert.equal((await api(CREATE, '{"name":"report.md","path":"dist/report.md"}')).status, 200)
  assert.deepEqual(JSON.parse(upstream.last().body), { name: 'report.md', content: Buffer.from('# Report\n').toString('base64') })
  assert.equal((await api(CREATE, '{"name":"report.md","path":"/workspace/dist/report.md"}')).status, 200, 'the Cloud\'s /workspace path names the Work root')

  const before = upstream.seen.length
  // Refused as the native API refuses a Cloud Work's source.
  for (const [path, status, code] of [['inputs/brief.md', 400, 'invalid_argument'], ['../outside.md', 400, 'invalid_argument'], ['missing.md', 404, 'not_found']] as const) {
    const refused = await api(CREATE, JSON.stringify({ name: 'x.md', path }))
    assert.equal(refused.status, status, path)
    assert.equal(JSON.parse(refused.body).code, code, path)
  }
  assert.equal(upstream.seen.length, before, 'a refused command reaches nothing')
})

test('an Artifact command the proxy does not complete passes unchanged', async () => {
  for (const body of ['{"name":"report.md","content":"eA=="}', '{"name":"report.md"}', 'not json']) {
    assert.equal((await api(CREATE, body)).status, 200, body)
    assert.equal(upstream.last().body, body, 'the API answers it')
  }
})

test('the run token, delivered credentials and a served API are required', async () => {
  assert.equal((await api(CREATE, '{}', 'wrong')).status, 401)
  assert.equal((await send(`${proxy.origin()}/w/w-1/api`)).status, 404)
  const escape = await send(proxy.origin(), { method: 'POST', rawPath: `/w/w-1/api/${TOKEN}/../../mcp`, body: '{}' })
  assert.equal(escape.status, 404, 'a path stays below the API')
  credentials = { gateway: credentials!.gateway }
  assert.equal((await api(CREATE, '{}')).status, 404, 'a Work whose platform serves no direct API')
  credentials = undefined
  assert.equal((await api(CREATE, '{}')).status, 503)
  assert.equal(upstream.seen.length, 0)
})
