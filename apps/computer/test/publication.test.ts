import assert from 'node:assert/strict'
import { chmod, mkdir, mkdtemp, rm, symlink, truncate, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import type { IncomingMessage } from 'node:http'
import { PassThrough } from 'node:stream'
import { completeRequest, type Completion } from '../src/proxy/publication.ts'
import { MAX_REQUEST_BYTES, MAX_SOURCE_BYTES, UNHELD_REQUEST_BYTES } from '../src/proxy/publication-source.ts'
import { readHead } from '../src/proxy/request-body.ts'
import { LocalProxy } from '../src/proxy/server.ts'
import { send, Upstream } from './support/upstream.ts'
import { posixModes } from './support/platform.ts'

const IDENTITY_TOKEN = 'agent-identity-token'

// Publication: the local proxy adds the
// bytes of a publish_artifact or replace_artifact call's source_path, read
// under the Work root, as source_content before the call reaches the
// platform's MCP endpoint.

const TOKEN = 'run-token'
const CONTENT = '# Report\nwith bytes é\0\n'

let dir: string
let root: string
let upstream: Upstream
let proxy: LocalProxy
const logs: string[] = []

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'publication-'))
  root = join(dir, 'works', 'w-1')
  await mkdir(join(root, 'out'), { recursive: true })
  await writeFile(join(root, 'out', 'report.md'), CONTENT)
  await writeFile(join(dir, 'outside.md'), 'outside')
  logs.length = 0
  upstream = new Upstream()
  await upstream.listen()
  proxy = new LocalProxy({
    portFile: join(dir, 'state', 'proxy.json'),
    access: {
      tokenValid: async (_workId, token) => token === TOKEN,
      credentials: async () => ({ gateway: { url: `${upstream.origin}/computer/v1/mcp`, identityToken: IDENTITY_TOKEN } }),
      modelProxy: async () => false,
      workRoot: (workId) => join(dir, 'works', workId),
    },
    log: (message) => logs.push(message),
  })
  await proxy.start()
})
afterEach(async () => {
  await proxy.close()
  await upstream.close()
  await rm(dir, { recursive: true, force: true })
})

const call = (id: number, name: string, args: Record<string, unknown>) =>
  ({ jsonrpc: '2.0', id, method: 'tools/call', params: { name, arguments: args } })

async function post(body: string): Promise<{ status: number; answer: string; seen: string | undefined; length: string | undefined }> {
  const before = upstream.seen.length
  const answer = await send(`${proxy.origin()}/w/w-1/mcp`, {
    method: 'POST',
    headers: { authorization: `Bearer ${TOKEN}`, 'content-type': 'application/json', 'content-length': Buffer.byteLength(body) },
    body,
  })
  const seen = upstream.seen.length > before ? upstream.last() : undefined
  return { status: answer.status, answer: answer.body, seen: seen?.body, length: seen?.headers['content-length'] }
}

const base64 = Buffer.from(CONTENT).toString('base64')

test('a publish_artifact call is completed with the source file bytes', async () => {
  for (const sourcePath of ['/workspace/out/report.md', 'out/report.md', './out/../out/report.md', join(root, 'out', 'report.md')]) {
    const body = JSON.stringify(call(1, 'publish_artifact', { source_path: sourcePath, title: 'Report' }))
    const { status, seen = '', length } = await post(body)
    assert.equal(status, 200)
    const forwarded = JSON.parse(seen)
    assert.deepEqual(forwarded, call(1, 'publish_artifact', { source_path: sourcePath, title: 'Report', source_content: base64 }), sourcePath)
    assert.equal(length, String(Buffer.byteLength(seen)), 'Content-Length matches the completed body')
  }
  for (const line of logs) assert.ok(!line.includes(base64))
})

test('source_content the Work supplies is replaced or removed, never forwarded', async () => {
  const supplied = Buffer.from('forged').toString('base64')
  const completed = await post(JSON.stringify(call(1, 'publish_artifact', { source_path: 'out/report.md', source_content: supplied })))
  assert.equal(JSON.parse(completed.seen ?? '').params.arguments.source_content, base64)
  const pathless = await post(JSON.stringify(call(1, 'replace_artifact', { artifact_name: 'a', source_content: supplied })))
  assert.deepEqual(JSON.parse(pathless.seen ?? ''), call(1, 'replace_artifact', { artifact_name: 'a' }), 'the Gateway refuses the missing path')
})

test('every publication call of a batch is completed, others pass as they are', async () => {
  const batch = [
    call(1, 'replace_artifact', { artifact_id: 'a-1', source_path: '/workspace/out/report.md' }),
    call(2, 'other_tool', { source_path: 'out/report.md' }),
    { jsonrpc: '2.0', method: 'notifications/initialized' },
    call(3, 'publish_artifact', { source_path: 'out/report.md' }),
  ]
  const { seen = '' } = await post(JSON.stringify(batch))
  const forwarded = JSON.parse(seen) as ReturnType<typeof call>[]
  assert.equal(forwarded[0].params.arguments.source_content, base64)
  assert.equal(forwarded[3].params.arguments.source_content, base64)
  assert.deepEqual(forwarded.slice(1, 3), batch.slice(1, 3))
})

test('a call that cannot be completed is answered with a JSON-RPC error and never forwarded', async () => {
  await writeFile(join(root, 'locked.md'), 'locked')
  await chmod(join(root, 'locked.md'), 0o000)
  await symlink(join(dir, 'outside.md'), join(root, 'escape.md'))
  await writeFile(join(root, 'out', 'huge.bin'), '')
  await truncate(join(root, 'out', 'huge.bin'), MAX_SOURCE_BYTES + 1)
  await mkdir(join(root, 'inputs'))
  await writeFile(join(root, 'inputs', 'brief.md'), 'input')
  await symlink(join(root, 'inputs', 'brief.md'), join(root, 'out', 'brief-link.md'))
  await symlink(join(root, 'out', 'report.md'), join(root, 'out', 'report-link.md'))
  await mkdir(join(root, 'knowledge'))
  await writeFile(join(root, 'knowledge', 'notes.md'), 'knowledge')
  // As on a Cloud: one regular file of the Work, reached without symbolic
  // links, outside the directories the Work only reads.
  const refused: [string, RegExp][] = [
    ['out/huge.bin', /larger than 64 MiB/],
    ['missing.md', /names no file/],
    ...[...(posixModes ? ['locked.md'] : []), '../outside.md', '/workspace/../outside.md', join(dir, 'outside.md'), 'escape.md', 'out', '/workspace', '', '/etc/hostname', 'out/brief-link.md', 'out/report-link.md']
      .map((path): [string, RegExp] => [path, /not one regular file/]),
    ...['inputs/brief.md', '/workspace/inputs/brief.md'].map((path): [string, RegExp] => [path, /under inputs\//]),
    ['knowledge/notes.md', /under knowledge\//],
  ]
  for (const [sourcePath, reason] of refused) {
    const { status, answer, seen } = await post(JSON.stringify(call(7, 'publish_artifact', { source_path: sourcePath, source_content: 'eA==' })))
    assert.equal(seen, undefined, `${sourcePath} is not forwarded`)
    assert.equal(status, 200)
    const { id, error } = JSON.parse(answer)
    assert.equal(id, 7)
    assert.equal(error.code, -32602)
    assert.match(error.message, reason, sourcePath)
  }
  // A batch with a refused call is answered whole: no part of it is forwarded.
  const batch = [
    call(1, 'publish_artifact', { source_path: 'out/report.md' }),
    { jsonrpc: '2.0', method: 'notifications/initialized' },
    call(2, 'publish_artifact', { source_path: 'missing.md' }),
  ]
  const answered = await post(JSON.stringify(batch))
  assert.equal(answered.seen, undefined)
  assert.deepEqual(JSON.parse(answered.answer).map((answer: { id: number; error: { code: number } }) => [answer.id, answer.error.code]), [[1, -32600], [2, -32602]])
  for (const body of [
    JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' }),
    'not json',
  ]) {
    assert.equal((await post(body)).seen, body)
  }
})

test('the files completed into one request stay within the Gateway\'s request limit', async () => {
  const half = Math.floor(MAX_SOURCE_BYTES * 0.6)
  for (const name of ['a.bin', 'b.bin']) {
    await writeFile(join(root, 'out', name), '')
    await truncate(join(root, 'out', name), half)
  }
  const batch = [call(1, 'publish_artifact', { source_path: 'out/a.bin' }), call(2, 'publish_artifact', { source_path: 'out/b.bin' })]
  const { answer, seen } = await post(JSON.stringify(batch))
  assert.equal(seen, undefined)
  const [first, second] = JSON.parse(answer) as { id: number; error: { code: number; message: string } }[]
  assert.deepEqual([first.id, first.error.code], [1, -32600])
  assert.deepEqual([second.id, second.error.code], [2, -32602])
  assert.match(second.error.message, /size limit/)
  // Each alone fits.
  const alone = await post(JSON.stringify(batch[1]))
  assert.equal(JSON.parse(alone.seen ?? '').params.arguments.source_content.length, 4 * Math.ceil(half / 3))
})

test('a body longer than the Gateway takes streams through unchanged', async () => {
  const padding = 'x'.repeat(MAX_REQUEST_BYTES + 10)
  const body = JSON.stringify(call(1, 'publish_artifact', { source_path: 'out/report.md', padding }))
  const { status, seen } = await post(body)
  assert.equal(status, 200)
  assert.equal(seen, body)
})

test('a body past the unheld bound is read further only while it holds one of two completion slots', async () => {
  const request = () => Object.assign(new PassThrough(), { headers: {} }) as unknown as IncomingMessage & PassThrough
  const settled = (completion: Promise<Completion>) => {
    const state = { done: false }
    void completion.then(() => { state.done = true })
    return state
  }
  const tick = () => new Promise((resolve) => setTimeout(resolve, 20))
  const piece = Buffer.alloc(64 * 1024, 0x20)
  const pieces = UNHELD_REQUEST_BYTES / piece.length + 4
  const writeLarge = (stream: PassThrough) => { for (let i = 0; i < pieces; i++) stream.write(piece) }
  const holders = [request(), request()]
  const held = holders.map((stream) => completeRequest(stream, root))
  for (const stream of holders) writeLarge(stream)
  await tick()

  const waiting = request()
  const waited = completeRequest(waiting, root)
  const state = settled(waited)
  writeLarge(waiting)
  waiting.end()
  await tick()
  assert.equal(state.done, false, 'a third large body waits for a slot before it is read further')
  assert.ok(waiting.readableLength > 0, 'its rest stays unread')

  const small = request()
  const smallDone = completeRequest(small, root)
  small.end('{"jsonrpc":"2.0","method":"ping","id":1}')
  assert.equal((await smallDone).kind, 'pass', 'a small body without publication calls needs no slot')

  holders[0].end()
  assert.equal((await held[0]).kind, 'pass')
  const completion = await waited
  assert.equal(completion.kind, 'pass')
  assert.equal(completion.kind === 'pass' && Buffer.concat(completion.body.chunks).length, pieces * piece.length)
  holders[1].end()
  await held[1]
})

test('a body that stalls is abandoned, so it cannot hold a completion slot', async () => {
  const stream = new PassThrough()
  const read = readHead(stream, MAX_REQUEST_BYTES, 50)
  stream.write('{"jsonrpc":')
  await assert.rejects(read, /body stalled/)
  assert.equal(stream.destroyed, true, 'the stalled request is closed')
})

test('a publication call in a body past the unheld bound is completed', async () => {
  const body = JSON.stringify(call(1, 'publish_artifact', { source_path: 'out/report.md', padding: 'x'.repeat(UNHELD_REQUEST_BYTES) }))
  const { seen } = await post(body)
  assert.equal(Buffer.from(JSON.parse(seen ?? '').params.arguments.source_content, 'base64').toString(), CONTENT)
})
