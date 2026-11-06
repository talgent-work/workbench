import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, test } from 'node:test'
import type { PipeInput, PipeSink } from '../src/execd/pipe.ts'
import { WorkInputs } from '../src/execd/inputs.ts'
import { ProcessTable } from '../src/execd/processes.ts'
import { ExecdService } from '../src/execd/service.ts'
import { SHIM_ARG } from '../src/execd/shim.ts'
import { Works } from '../src/execd/works.ts'
import { CredentialStore } from '../src/proxy/credentials.ts'
import { posixOnly } from './support/platform.ts'

const MAIN = fileURLToPath(new URL('../src/main.ts', import.meta.url))

let dir: string
let service: ExecdService
let processes: ProcessTable
const created: string[] = []

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'service-'))
  processes = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [process.execPath, MAIN, SHIM_ARG] })
  service = new ExecdService({
    works: new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') }),
    processes,
    claudeBinDir: '/opt/claude-code/bin',
    credentials: new CredentialStore({ dir: join(dir, 'state', 'credentials') }),
    inputs: new WorkInputs({ recordsDir: join(dir, 'state', 'inputs'), credentials: { get: async () => undefined }, log: () => {} }),
    proxyOrigin: () => 'http://127.0.0.1:1',
    baseEnv: { PATH: '/usr/bin:/bin', MACHINE: 'm', HOME: '/ignored' },
    home: '/home/owner',
    log: () => {},
  })
  const put = await service.request({ workId: 'w-1', method: 'PUT', path: '/', token: '', body: { access_token: 'tok', environment: { A: 'from-work', HOME: '/work-home' } } })
  assert.deepEqual(put, { status: 200, body: { work_root: join(dir, 'works', 'w-1') } })
})
afterEach(async () => {
  for (const id of created.splice(0)) await processes.delete('w-1', id)
  await rm(dir, { recursive: true, force: true })
})

const call = (method: string, path: string, body?: unknown, token = 'tok', workId = 'w-1') =>
  service.request({ workId, method, path, token, body })

async function create(command: string, cwd = '/workspace'): Promise<string> {
  const response = await call('POST', '/pty', { cwd, command })
  assert.equal(response.status, 201)
  const id = (response.body as { session_id: string }).session_id
  created.push(id)
  return id
}

function sink() {
  const closed = new AbortController()
  const texts: string[] = []
  const binaries: Buffer[] = []
  let ended = false
  const value: PipeSink = {
    closed: closed.signal,
    text: (text) => texts.push(text),
    binary: (frame) => binaries.push(frame),
    end: () => { ended = true },
    writable: async () => {},
  }
  return { value, texts, binaries, closed, ended: () => ended }
}

async function until(check: () => boolean, timeoutMs = 10_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (!check()) {
    if (Date.now() > deadline) throw new Error('timed out')
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
}

test('calls of a Work need its token; unknown Works and paths are not found', async () => {
  assert.deepEqual(await call('GET', '/ping'), { status: 200, body: { reconciled: true } }, 'before any connection the round is decided')
  assert.equal((await call('GET', '/ping', undefined, 'wrong')).status, 401)
  assert.equal((await call('GET', '/ping', undefined, 'tok', 'w-2')).status, 404)
  assert.equal((await call('GET', '/nothing')).status, 404)
  assert.equal((await service.request({ workId: '../x', method: 'PUT', path: '/', token: '', body: { access_token: 't' } })).status, 404)
  assert.equal((await service.request({ workId: 'w-1', method: 'PUT', path: '/', token: '', body: {} })).status, 400)
})

test('create only reserves: nothing runs before the first attach', async () => {
  const id = await create('touch started')
  assert.deepEqual(await call('GET', `/pty/${id}`), { status: 200, body: { session_id: id, running: false, output_offset: 0 } })
  await new Promise((resolve) => setTimeout(resolve, 200))
  assert.equal(existsSync(join(dir, 'works', 'w-1', 'started')), false)
  assert.equal(existsSync(join(processes.dir('w-1', id), 'started.json')), false)
})

test('a process runs as sent in the mapped cwd with the machine, Work and Talgent environment', { skip: posixOnly('Git Bash prints POSIX-style paths') }, async () => {
  const root = join(dir, 'works', 'w-1')
  await writeFile(join(root, 'marker'), 'found\n')
  const id = await create('pwd; echo "$TALGENT_WORK_ROOT|$HOME|$A|$MACHINE|$PATH"; cat "$TALGENT_WORK_ROOT/marker"; echo /workspace', '/workspace/sub')
  const out = sink()
  const pipe = await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, out.value)
  assert.ok(!('status' in pipe))
  await until(() => out.ended())
  const stdout = out.binaries.filter((frame) => frame[0] === 1).map((frame) => frame.subarray(1).toString()).join('')
  assert.equal(stdout, `${root}/sub\n${root}|/home/owner|from-work|m|/opt/claude-code/bin:/usr/bin:/bin\nfound\n/workspace\n`, 'the command runs as sent')
  assert.equal(out.texts.at(-1), '{"type":"exit","exit_code":0}\n')
  const status = await call('GET', `/pty/${id}`)
  assert.deepEqual(status.body, { session_id: id, running: false, output_offset: Buffer.byteLength(stdout) })
})

test('one pipe holds a process; a second open answers 409 until the first closes', async () => {
  const id = await create('read x')
  const first = sink()
  const input = (await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, first.value)) as PipeInput
  await until(() => first.texts.length > 0)
  assert.deepEqual(await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, sink().value), { status: 409 })
  assert.deepEqual(await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'bad' }, sink().value), { status: 401 })
  assert.deepEqual(await service.openStream({ workId: 'w-1', processId: 'nope', since: 0, token: 'tok' }, sink().value), { status: 404 })
  first.closed.abort()
  // The holder lets go once its close is handled, which takes a moment.
  const second = sink()
  let again = await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, second.value)
  for (let tries = 0; 'status' in again && again.status === 409 && tries < 100; tries++) {
    await new Promise((resolve) => setTimeout(resolve, 50))
    again = await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, second.value)
  }
  assert.ok(!('status' in again), 'the process opens again once the first pipe closed')
  void input
  again.binary(Buffer.from('\0line\n'))
  await until(() => second.texts.some((text) => text.includes('"exit"')))
})

test('delete forgets a reservation and is not found the second time', async () => {
  const id = await create('true')
  assert.deepEqual(await call('DELETE', `/pty/${id}`), { status: 200 })
  assert.equal(existsSync(processes.dir('w-1', id)), false)
  assert.equal((await call('DELETE', `/pty/${id}`)).status, 404)
  assert.equal((await call('GET', `/pty/${id}`)).status, 404)
})

test('a cwd reached through a symlink is the cwd the process sees, as named', { skip: posixOnly('Git Bash prints POSIX-style paths') }, async () => {
  // As macOS's /var, which is /private/var.
  const root = join(dir, 'works', 'w-1')
  await mkdir(join(root, 'real'))
  await symlink(join(root, 'real'), join(root, 'link'))
  const id = await create('pwd', '/workspace/link')
  const out = sink()
  const pipe = await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, out.value)
  assert.ok(!('status' in pipe))
  await until(() => out.ended())
  const stdout = out.binaries.filter((frame) => frame[0] === 1).map((frame) => frame.subarray(1).toString()).join('')
  assert.equal(stdout, `${root}/link\n`)
})
