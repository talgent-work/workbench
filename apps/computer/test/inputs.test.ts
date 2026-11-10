import assert from 'node:assert/strict'
import { chmod, mkdtemp, readdir, readFile, rm, stat, utimes, writeFile } from 'node:fs/promises'
import { createServer, type IncomingHttpHeaders, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { parseManifest, RETRY_DELAYS_MS, WorkInputs } from '../src/execd/inputs.ts'
import type { PipeSink } from '../src/execd/pipe.ts'
import { ProcessTable } from '../src/execd/processes.ts'
import { ExecdService } from '../src/execd/service.ts'
import { SHIM_ARG } from '../src/execd/shim.ts'
import { Works } from '../src/execd/works.ts'
import type { WorkCredentials } from '../src/proxy/credentials.ts'
import { posixModes } from './support/platform.ts'

const IDENTITY_TOKEN = 'agent-identity-token'

// Work inputs: manifests relayed to the
// execd service, files fetched from a stand-in Storage on the platform's
// origin into the Work root's read-only inputs directory.


// Serves /files/<name> from contents; a handler may take over a name.
class Storage {
  origin = ''
  readonly contents = new Map<string, string>()
  readonly handlers = new Map<string, (response: ServerResponse) => void>()
  readonly seen: { url: string; headers: IncomingHttpHeaders }[] = []
  private readonly server = createServer((request, response) => {
    this.seen.push({ url: request.url ?? '', headers: request.headers })
    const name = decodeURIComponent((request.url ?? '').replace(/^\/files\//, '').replace(/\?.*$/, ''))
    const handler = this.handlers.get(name)
    if (handler !== undefined) return handler(response)
    const content = this.contents.get(name)
    if (content === undefined) return void response.writeHead(404).end()
    response.writeHead(200, { 'Content-Length': Buffer.byteLength(content) }).end(content)
  })

  async listen(): Promise<void> {
    await new Promise<void>((resolve) => this.server.listen(0, '127.0.0.1', resolve))
    this.origin = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`
  }

  async close(): Promise<void> {
    const closed = new Promise<void>((resolve) => this.server.close(() => resolve()))
    this.server.closeAllConnections()
    await closed
  }
}

let dir: string
let storage: Storage
let credentials: WorkCredentials | undefined
let inputs: WorkInputs
let service: ExecdService
const logs: string[] = []
const root = () => join(dir, 'works', 'w-1')
const inputsDir = () => join(root(), 'inputs')

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'inputs-'))
  logs.length = 0
  storage = new Storage()
  await storage.listen()
  credentials = { gateway: { url: `${storage.origin}/computer/v1/mcp`, identityToken: IDENTITY_TOKEN, spaceToken: 'space-1' } }
  inputs = new WorkInputs({
    recordsDir: join(dir, 'state', 'inputs'),
    credentials: { get: async () => credentials },
    log: (message) => logs.push(message),
    timeouts: { connectMs: 2000, idleMs: 300 },
  })
  const works = new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') })
  service = new ExecdService({
    works,
    processes: undefined as never,
    credentials: { put: async () => {}, delete: async () => {} } as never,
    inputs,
    proxyOrigin: () => 'http://127.0.0.1:1',
    baseEnv: {},
    home: '/home/owner',
    log: () => {},
  })
  assert.equal((await service.request({ workId: 'w-1', method: 'PUT', path: '/', token: '', body: { access_token: 'tok' } })).status, 200)
})
afterEach(async () => {
  await storage.close()
  // The read-only inputs directory keeps its files from a plain removal.
  await chmod(inputsDir(), 0o755).catch(() => {})
  await rm(dir, { recursive: true, force: true })
})

function entry(name: string, updatedAt = '2026-11-02T10:00:00Z', origin = storage.origin): Record<string, unknown> {
  const content = storage.contents.get(name) ?? ''
  return { name, size: Buffer.byteLength(content), updated_at: updatedAt, url: `${origin}/files/${encodeURIComponent(name)}?sig=secret-signature` }
}

async function put(files: Record<string, unknown>[]): Promise<void> {
  assert.deepEqual(await service.request({ workId: 'w-1', method: 'PUT', path: '/inputs', token: '', body: { files } }), { status: 202 })
  await inputs.settled('w-1')
}

// Submits a manifest straight to the Work's inputs.
const submit = (files: Record<string, unknown>[]) => inputs.submit('w-1', root(), parseManifest({ files }))

const listing = async () => (await readdir(inputsDir())).sort()
const read = (name: string) => readFile(join(inputsDir(), name), 'utf8')

test('a manifest brings its files into a read-only inputs directory, sending no identity with its Access Links', async () => {
  storage.contents.set('brief.pdf', 'brief v1')
  storage.contents.set('.hidden notes.txt', 'notes')
  await put([entry('brief.pdf'), entry('.hidden notes.txt')])
  assert.deepEqual(await listing(), ['.hidden notes.txt', 'brief.pdf'])
  assert.equal(await read('brief.pdf'), 'brief v1')
  if (posixModes) {
    assert.equal((await stat(join(inputsDir(), 'brief.pdf'))).mode & 0o777, 0o444)
    assert.equal((await stat(inputsDir())).mode & 0o777, 0o555)
    await assert.rejects(writeFile(join(inputsDir(), 'new'), 'x'), { code: 'EACCES' })
  }
  const headers = storage.seen[0].headers
  for (const name of ['authorization', 'x-talgent-work-id', 'x-talgent-work-credential', 'x-talgent-space-token']) assert.equal(headers[name], undefined, name)
  for (const line of logs) assert.ok(!line.includes(IDENTITY_TOKEN) && !line.includes('secret-signature'), line)
})

test('only changed files are fetched again, and unlisted ones are removed', async () => {
  storage.contents.set('a.txt', 'a1')
  storage.contents.set('b.txt', 'b1')
  await put([entry('a.txt'), entry('b.txt')])
  assert.equal(storage.seen.length, 2)
  await put([entry('a.txt'), entry('b.txt')])
  assert.equal(storage.seen.length, 2, 'an unchanged manifest fetches nothing')
  storage.contents.set('a.txt', 'a2')
  await put([entry('a.txt', '2026-11-02T11:00:00Z')])
  assert.equal(storage.seen.length, 3)
  assert.deepEqual(await listing(), ['a.txt'])
  assert.equal(await read('a.txt'), 'a2')
  assert.equal((await stat(join(inputsDir(), 'a.txt'))).mtime.toISOString(), '2026-11-02T11:00:00.000Z', 'the copy carries updated_at')
  // A copy removed or changed on the machine, even at the same size, is
  // restored though the manifest is not.
  await chmod(inputsDir(), 0o755)
  await rm(join(inputsDir(), 'a.txt'))
  await chmod(inputsDir(), 0o555)
  await put([entry('a.txt', '2026-11-02T11:00:00Z')])
  assert.equal(await read('a.txt'), 'a2')
  await chmod(join(inputsDir(), 'a.txt'), 0o644)
  await writeFile(join(inputsDir(), 'a.txt'), 'zz')
  await put([entry('a.txt', '2026-11-02T11:00:00Z')])
  assert.equal(await read('a.txt'), 'a2', 'a same-size edit')
  const fetched = storage.seen.length
  await utimes(join(inputsDir(), 'a.txt'), new Date('2026-11-02T11:00:00Z'), new Date('2026-11-02T11:00:00Z'))
  await put([entry('a.txt', '2026-11-02T11:00:00Z')])
  assert.equal(storage.seen.length, fetched, 'a copy at the listed size and time is current')
  await put([])
  assert.deepEqual(await listing(), [])
})

test('a file off the platform origin is not fetched and the identity never leaves', async () => {
  const other = new Storage()
  await other.listen()
  try {
    other.contents.set('x.txt', 'elsewhere')
    storage.contents.set('x.txt', 'elsewhere')
    await put([entry('x.txt', undefined, other.origin)])
    assert.equal(other.seen.length, 0)
    assert.deepEqual(await listing(), [])
    assert.ok(logs.some((line) => line.includes('not on the platform\'s origin')))
  } finally {
    await other.close()
  }
})

test('without credentials the downloads wait and run once the credentials arrive', async () => {
  credentials = undefined
  storage.contents.set('a.txt', 'a1')
  await put([entry('a.txt')])
  assert.deepEqual(await listing(), [])
  assert.equal(storage.seen.length, 0)
  assert.ok(logs.some((line) => line.includes('wait for the Work\'s credentials')))
  credentials = { gateway: { url: `${storage.origin}/computer/v1/mcp`, identityToken: IDENTITY_TOKEN } }
  inputs.credentialsDelivered('w-1')
  await inputs.settled('w-1')
  assert.equal(await read('a.txt'), 'a1')
  assert.equal(storage.seen[0].headers['x-talgent-space-token'], undefined)
})

test('a failed download keeps the previous copy and the next manifest fetches it', async () => {
  storage.contents.set('a.txt', 'a1')
  await put([entry('a.txt')])
  storage.contents.set('a.txt', 'a2 longer')
  const failures: [string, (response: ServerResponse) => void][] = [
    ['answered 500', (response) => void response.writeHead(500).end()],
    ['larger than listed', (response) => void response.writeHead(200).end('a2 longer and longer')],
    ['smaller than listed', (response) => void response.writeHead(200).end('a2')],
    ['stalled', (response) => { response.writeHead(200); response.write('a2') }],
  ]
  for (const [reason, handler] of failures) {
    storage.handlers.set('a.txt', handler)
    await put([entry('a.txt', '2026-11-02T12:00:00Z')])
    assert.equal(await read('a.txt'), 'a1', reason)
    assert.ok(logs.at(-1)?.includes(reason), `${reason}: ${logs.at(-1)}`)
    assert.deepEqual(await listing(), ['a.txt'], 'no temporary file is left')
  }
  storage.handlers.clear()
  await put([entry('a.txt', '2026-11-02T12:00:00Z')])
  assert.equal(await read('a.txt'), 'a2 longer')
})

test('a newer manifest supersedes the one in progress', async () => {
  let release: () => void = () => {}
  storage.contents.set('slow.bin', 'slow')
  storage.contents.set('next.txt', 'next')
  storage.handlers.set('slow.bin', (response) => {
    response.writeHead(200)
    response.write('sl')
    release = () => response.end('ow')
  })
  const first = await service.request({ workId: 'w-1', method: 'PUT', path: '/inputs', token: '', body: { files: [entry('slow.bin')] } })
  assert.equal(first.status, 202, 'answered before the download ends')
  while (storage.seen.length === 0) await new Promise((resolve) => setTimeout(resolve, 5))
  await put([entry('next.txt')])
  release()
  assert.deepEqual(await listing(), ['next.txt'])
  assert.ok(logs.every((line) => !line.includes('slow.bin')), 'a superseded download is not a failure')
})

test('a manifest listing the same versions with fresh links lets the download in progress end', async () => {
  let release: () => void = () => {}
  storage.contents.set('large.bin', 'large')
  storage.handlers.set('large.bin', (response) => {
    response.writeHead(200)
    response.write('lar')
    release = () => response.end('ge')
  })
  submit([entry('large.bin')])
  while (storage.seen.length === 0) await new Promise((resolve) => setTimeout(resolve, 5))
  const refreshed = { ...entry('large.bin'), url: `${storage.origin}/files/large.bin?sig=fresh` }
  submit([refreshed])
  release()
  await inputs.settled('w-1')
  assert.equal(await read('large.bin'), 'large')
  assert.equal(storage.seen.length, 1, 'the refreshed manifest finds the copy current and fetches nothing')
  assert.ok(logs.every((line) => !line.includes('large.bin')), 'the download in progress is not cut off')
})

test('the inputs PUT validates its manifest and knows only open Works', async () => {
  const call = (body: unknown, workId = 'w-1', method = 'PUT') => service.request({ workId, method, path: '/inputs', token: '', body })
  const good = { name: 'a', size: 1, updated_at: '2026-11-02T10:00:00Z', url: 'https://x/a' }
  for (const body of [
    null, {}, { files: {} }, { files: [null] },
    ...['', '.', '..', 'a/b', '..\\x', 'a\0b', 'x.talgent-download', 'x'.repeat(256)].map((name) => ({ files: [{ ...good, name }] })),
    { files: [{ ...good, size: -1 }] }, { files: [{ ...good, size: 1.5 }] }, { files: [{ ...good, updated_at: '' }] }, { files: [{ ...good, updated_at: 'yesterday' }] },
    { files: [{ ...good, url: 'ftp://x/a' }] }, { files: [{ ...good, url: 'nope' }] }, { files: [good, good] },
  ]) {
    const answer = await call(body)
    assert.equal(answer.status, 400, JSON.stringify(body))
    assert.equal((answer.body as { code: string }).code, 'INVALID_REQUEST_BODY')
  }
  assert.equal((await call({ files: [] }, 'w-unknown')).status, 404)
  assert.equal((await call({ files: [] }, 'w-1', 'GET')).status, 405)
})

test('every retry of a manifest runs while its Access Links last', () => {
  const accessLinkLifetimeMs = 15 * 60_000
  assert.ok(RETRY_DELAYS_MS.reduce((total, delay) => total + delay, 0) < accessLinkLifetimeMs)
})

test('a failed download is retried while its manifest is the latest, a bounded number of times', async () => {
  inputs = new WorkInputs({
    recordsDir: join(dir, 'state', 'inputs'),
    credentials: { get: async () => credentials },
    log: (message) => logs.push(message),
    timeouts: { connectMs: 2000, idleMs: 300 },
    retryDelaysMs: [20, 20],
  })
  storage.contents.set('a.txt', 'a1')
  let failures = 0
  storage.handlers.set('a.txt', (response) => { failures += 1; response.writeHead(500).end() })
  submit([entry('a.txt')])
  while (failures < 3) await new Promise((resolve) => setTimeout(resolve, 5))
  await new Promise((resolve) => setTimeout(resolve, 100))
  await inputs.settled('w-1')
  assert.equal(failures, 3, 'the first try and two retries')
  assert.ok(logs.some((line) => line.includes('retries ran out')))

  // A retry succeeds; a newer manifest or withdrawn credentials cancel one.
  failures = 0
  storage.handlers.set('a.txt', (response) => {
    failures += 1
    if (failures === 1) return void response.writeHead(500).end()
    response.writeHead(200).end('a1')
  })
  submit([entry('a.txt')])
  while (failures < 2) await new Promise((resolve) => setTimeout(resolve, 5))
  await inputs.settled('w-1')
  assert.equal(await read('a.txt'), 'a1')
  for (const cancel of [() => submit([]), () => inputs.forget('w-1')]) {
    failures = 0
    storage.handlers.set('b.txt', (response) => { failures += 1; response.writeHead(500).end() })
    storage.contents.set('b.txt', 'b1')
    submit([entry('b.txt')])
    await inputs.settled('w-1')
    cancel()
    await inputs.settled('w-1')
    await new Promise((resolve) => setTimeout(resolve, 100))
    assert.equal(failures, 1, 'no retry after the cancel')
  }
})

test('credentials delivered while a download looks for them wake it', async () => {
  storage.contents.set('a.txt', 'a1')
  const lookups: ((value: WorkCredentials | undefined) => void)[] = []
  inputs = new WorkInputs({
    recordsDir: join(dir, 'state', 'inputs'),
    credentials: { get: () => new Promise((resolve) => lookups.push(resolve)) },
    log: (message) => logs.push(message),
  })
  const until = async (done: () => boolean) => { while (!done()) await new Promise((resolve) => setTimeout(resolve, 5)) }
  submit([entry('a.txt')])
  await until(() => lookups.length === 1)
  // The delivery completes before the store answers the earlier read.
  inputs.credentialsDelivered('w-1')
  lookups[0](undefined)
  await until(() => lookups.length === 2)
  lookups[1](credentials)
  await inputs.settled('w-1')
  assert.equal(await read('a.txt'), 'a1')
})

test('a run waits for the inputs being brought in before it starts', async () => {
  const processes = new ProcessTable({
    stateRoot: join(dir, 'state', 'processes'),
    shim: [process.execPath, fileURLToPath(new URL('../src/main.ts', import.meta.url)), SHIM_ARG],
  })
  const runs = new ExecdService({
    works: new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') }),
    processes,
    credentials: { put: async () => {}, delete: async () => {} } as never,
    inputs,
    proxyOrigin: () => 'http://127.0.0.1:1',
    baseEnv: { PATH: '/usr/bin:/bin' },
    home: '/home/owner',
    log: () => {},
  })
  storage.contents.set('brief.txt', 'the brief')
  let release: () => void = () => {}
  storage.handlers.set('brief.txt', (response) => {
    response.writeHead(200)
    release = () => response.end('the brief')
  })
  submit([entry('brief.txt')])
  const created = await runs.request({ workId: 'w-1', method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', command: 'cat inputs/brief.txt' } })
  const id = (created.body as { session_id: string }).session_id
  const frames: Buffer[] = []
  const sink: PipeSink = {
    closed: new AbortController().signal,
    text: () => {},
    binary: (frame) => frames.push(frame),
    end: () => {},
    writable: async () => {},
  }
  try {
    assert.ok(!('status' in (await runs.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, sink))))
    while (storage.seen.length === 0) await new Promise((resolve) => setTimeout(resolve, 5))
    await new Promise((resolve) => setTimeout(resolve, 100))
    assert.equal((await processes.phase('w-1', id))?.kind, 'reserved', 'not started while the download runs')
    release()
    const deadline = Date.now() + 10_000
    const stdout = () => frames.filter((frame) => frame[0] === 1).map((frame) => frame.subarray(1).toString()).join('')
    while (!stdout().includes('the brief') && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10))
    assert.equal(stdout(), 'the brief')
  } finally {
    await processes.delete('w-1', id)
  }
})

test('waiting for the inputs is bounded', async () => {
  storage.contents.set('slow.bin', 'slow')
  storage.handlers.set('slow.bin', (response) => { response.writeHead(200); response.write('sl') })
  submit([entry('slow.bin')])
  const started = Date.now()
  await inputs.settled('w-1', 50)
  assert.ok(Date.now() - started < 250, 'resolved by the bound while the download stalls')
  await inputs.settled('w-1')
})
