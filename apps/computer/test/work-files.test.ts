import assert from 'node:assert/strict'
import { mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { WorkInputs } from '../src/execd/inputs.ts'
import type { PipeSink } from '../src/execd/pipe.ts'
import { ExecdService } from '../src/execd/service.ts'
import { FILE_CHUNK_BYTES } from '../src/execd/work-files.ts'
import { Works } from '../src/execd/works.ts'

// Work files: listing a directory of the
// Work root and reading a file as a stream, both confined to the Work root.

let dir: string
let root: string
let service: ExecdService

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'work-files-'))
  service = new ExecdService({
    works: new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') }),
    processes: undefined as never,
    credentials: undefined as never,
    inputs: new WorkInputs({ recordsDir: join(dir, 'state', 'inputs'), credentials: { get: async () => undefined }, log: () => {} }),
    proxyOrigin: () => 'http://127.0.0.1:1',
    baseEnv: {},
    home: '/home/owner',
    log: () => {},
  })
  const put = await service.request({ workId: 'w-1', method: 'PUT', path: '/', token: '', body: { access_token: 'tok' } })
  root = (put.body as { work_root: string }).work_root
  await mkdir(join(root, 'sub dir'))
  await writeFile(join(root, 'a.txt'), 'alpha')
  await writeFile(join(root, 'sub dir', 'b.txt'), 'beta!')
  await mkdir(join(dir, 'outside'))
  await writeFile(join(dir, 'outside', 'secret'), 'secret')
  await symlink(join(dir, 'outside'), join(root, 'out-link'))
  await symlink(join(root, 'sub dir'), join(root, 'in-link'))
})
afterEach(async () => {
  await rm(dir, { recursive: true, force: true })
})

const list = (path: string, token = 'tok', workId = 'w-1') =>
  service.request({ workId, method: 'GET', path: `/files?path=${encodeURIComponent(path)}`, token })

test('a listing names the files and directories of a Work root directory, links that stay inside included', async () => {
  const answer = await list('')
  assert.equal(answer.status, 200)
  const entries = (answer.body as { entries: { name: string; kind: string; size: number; modified_at: string }[] }).entries
  assert.deepEqual(entries.map(({ name, kind, size }) => ({ name, kind, size })), [
    { name: 'a.txt', kind: 'file', size: 5 },
    { name: 'in-link', kind: 'directory', size: 0 },
    { name: 'sub dir', kind: 'directory', size: 0 },
  ])
  assert.ok(!Number.isNaN(Date.parse(entries[0].modified_at)))
  const sub = await list('sub dir')
  assert.deepEqual((sub.body as { entries: { name: string }[] }).entries.map((entry) => entry.name), ['b.txt'])
  assert.equal((await service.request({ workId: 'w-1', method: 'GET', path: '/files', token: 'tok' })).status, 200, 'no path is the root')
})

test('a listing outside the Work root, through a link or missing, is not found', async () => {
  for (const path of ['..', '../outside', 'out-link', 'missing', 'sub dir/../../outside', 'a\0b']) {
    assert.equal((await list(path)).status, 404, path)
  }
  assert.equal((await list('a.txt')).status, 400)
  assert.equal((await list('', 'bad')).status, 401)
  assert.equal((await list('', 'tok', 'w-2')).status, 404)
  assert.equal((await service.request({ workId: 'w-1', method: 'POST', path: '/files', token: 'tok' })).status, 405)
})

const statOf = (path: string, token = 'tok') =>
  service.request({ workId: 'w-1', method: 'GET', path: `/files/stat?path=${encodeURIComponent(path)}`, token })

test('a stat describes one file or directory of the Work root', async () => {
  const file = await statOf('sub dir/b.txt')
  assert.equal(file.status, 200)
  const { modified_at: modified, ...rest } = file.body as { modified_at: string }
  assert.deepEqual(rest, { name: 'b.txt', kind: 'file', size: 5 })
  assert.ok(!Number.isNaN(Date.parse(modified)))
  const linked = await statOf('in-link')
  assert.deepEqual({ ...(linked.body as object), modified_at: undefined }, { name: 'in-link', kind: 'directory', size: 0, modified_at: undefined })
  assert.equal(((await statOf('')).body as { kind: string }).kind, 'directory', 'no path is the root')
})

test('a stat outside the Work root, through a link or missing, is not found', async () => {
  for (const path of ['..', '../outside', 'out-link', 'out-link/secret', 'missing', 'sub dir/../../outside', 'a\0b']) {
    assert.equal((await statOf(path)).status, 404, path)
  }
  assert.equal((await statOf('a.txt', 'bad')).status, 401)
  assert.equal((await service.request({ workId: 'w-1', method: 'POST', path: '/files/stat?path=a.txt', token: 'tok' })).status, 405)
})

function sink(gate?: { open: boolean }) {
  const closed = new AbortController()
  const binaries: Buffer[] = []
  let ended: number | undefined
  const value: PipeSink = {
    closed: closed.signal,
    text: () => {},
    binary: (frame) => binaries.push(Buffer.from(frame)),
    end: (status = 0) => { ended = status },
    writable: async () => {
      while (gate !== undefined && !gate.open && !closed.signal.aborted) await new Promise((resolve) => setTimeout(resolve, 5))
    },
  }
  return { value, binaries, closed, ended: () => ended }
}

async function until(check: () => boolean): Promise<void> {
  const deadline = Date.now() + 5000
  while (!check()) {
    if (Date.now() > deadline) throw new Error('timed out')
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
}

const read = (path: string, out: ReturnType<typeof sink>, token = 'tok', workId = 'w-1') =>
  service.openStream({ kind: 'file', workId, path, token }, out.value)

test('a file read streams its bytes in chunks of at most 64 KiB, then ends normally', async () => {
  const big = Buffer.alloc(FILE_CHUNK_BYTES * 3 + 17)
  for (let i = 0; i < big.length; i++) big[i] = i % 251
  await writeFile(join(root, 'sub dir', 'big.bin'), big)
  for (const path of ['sub dir/big.bin', 'in-link/big.bin', '/sub dir/big.bin']) {
    const out = sink()
    const opened = await read(path, out)
    assert.ok(!('status' in opened), path)
    await until(() => out.ended() !== undefined)
    assert.equal(out.ended(), 0)
    assert.ok(out.binaries.every((frame) => frame.length <= FILE_CHUNK_BYTES))
    assert.ok(Buffer.concat(out.binaries).equals(big), path)
  }
  const empty = sink()
  await writeFile(join(root, 'empty'), '')
  await read('empty', empty)
  await until(() => empty.ended() !== undefined)
  assert.deepEqual(empty.binaries, [])
})

test('a file read waits while the link holds back and stops when the reader leaves', async () => {
  await writeFile(join(root, 'big.bin'), Buffer.alloc(FILE_CHUNK_BYTES * 4))
  const gate = { open: true }
  const out = sink(gate)
  const original = out.value.binary
  out.value.binary = (frame) => {
    original(frame)
    gate.open = false
  }
  await read('big.bin', out)
  await until(() => out.binaries.length === 1)
  await new Promise((resolve) => setTimeout(resolve, 50))
  assert.equal(out.binaries.length, 1, 'nothing more while the link is full')
  gate.open = true
  await until(() => out.binaries.length === 2)
  out.closed.abort()
  gate.open = true
  await new Promise((resolve) => setTimeout(resolve, 50))
  assert.ok(out.binaries.length <= 3)
  assert.equal(out.ended(), undefined, 'a stream the reader closed is not ended again')
})

test('a refused file read answers its status before any data', async () => {
  await symlink(join(dir, 'outside', 'secret'), join(root, 'secret-link'))
  const cases: [string, number, string?, string?][] = [
    ['missing', 404], ['../outside/secret', 404], ['out-link/secret', 404], ['secret-link', 404],
    ['sub dir', 400], ['', 400], ['a.txt', 401, 'bad'], ['a.txt', 404, 'tok', 'w-2'],
  ]
  for (const [path, status, token, workId] of cases) {
    const out = sink()
    assert.deepEqual(await read(path, out, token, workId), { status }, path)
    assert.deepEqual(out.binaries, [])
  }
})
