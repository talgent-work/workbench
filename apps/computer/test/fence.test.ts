import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, test } from 'node:test'
import type { PipeSink } from '../src/execd/pipe.ts'
import { WorkInputs } from '../src/execd/inputs.ts'
import { ProcessTable, type RunningProcess } from '../src/execd/processes.ts'
import { ExecdService } from '../src/execd/service.ts'
import { SHIM_ARG } from '../src/execd/shim.ts'
import { Works } from '../src/execd/works.ts'
import { Fence, FENCE_AFTER_MS, type FenceOptions } from '../src/fence.ts'
import { CredentialStore } from '../src/proxy/credentials.ts'

const MAIN = fileURLToPath(new URL('../src/main.ts', import.meta.url))
const MINUTE = 60_000

let dir: string
let service: ExecdService
let processes: ProcessTable
let clock: number
let tick: () => void
let logs: string[]
const fences: Fence[] = []
const created: string[] = []

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'fence-'))
  clock = 1_700_000_000_000
  tick = () => assert.fail('no check scheduled')
  logs = []
  processes = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [process.execPath, MAIN, SHIM_ARG], uses: [join(dir, 'claude-code', '1.0.0')] })
  service = new ExecdService({
    works: new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') }),
    processes,
    credentials: new CredentialStore({ dir: join(dir, 'state', 'credentials') }),
    inputs: new WorkInputs({ recordsDir: join(dir, 'state', 'inputs'), credentials: { get: async () => undefined }, log: () => {} }),
    proxyOrigin: () => 'http://127.0.0.1:1',
    baseEnv: { PATH: process.env.PATH ?? '/usr/bin:/bin' },
    home: dir,
    log: () => {},
  })
  const put = await service.request({ workId: 'w-1', method: 'PUT', path: '/', token: '', body: { access_token: 'tok' } })
  assert.equal(put.status, 200)
})
afterEach(async () => {
  for (const fence of fences.splice(0)) await fence.stop()
  for (const id of created.splice(0)) await processes.delete('w-1', id)
  await rm(dir, { recursive: true, force: true })
})

// A fence on the test's clock, whose periodic check runs only through tick.
async function fence(overrides: Partial<FenceOptions> = {}): Promise<Fence> {
  const value = new Fence({
    stateFile: join(dir, 'state', 'fence.json'),
    processes,
    log: (line) => logs.push(line),
    now: () => clock,
    every: (check) => {
      tick = check
      return () => { tick = () => {} }
    },
    ...overrides,
  })
  fences.push(value)
  await value.start()
  return value
}

// A Work process that runs until it is ended: it waits for stdin.
async function running(): Promise<string> {
  const response = await service.request({ workId: 'w-1', method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', command: 'read x' } })
  assert.equal(response.status, 201)
  const id = (response.body as { session_id: string }).session_id
  created.push(id)
  const closed = new AbortController()
  const out: PipeSink = { closed: closed.signal, text: () => {}, binary: () => {}, end: () => {}, writable: async () => {} }
  const pipe = await service.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, out)
  assert.ok(!('status' in pipe))
  await until(async () => (await processes.phase('w-1', id))?.kind === 'running')
  closed.abort()
  return id
}

async function until(check: () => Promise<boolean>, timeoutMs = 10_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (!(await check())) {
    if (Date.now() > deadline) throw new Error('timed out')
    await new Promise((resolve) => setTimeout(resolve, 20))
  }
}

// The machine's processes, calling ended before each end.
function counting(ended: () => void): FenceOptions['processes'] {
  return {
    holdStarts: () => processes.holdStarts(),
    running: () => processes.running(),
    end: (targets) => { ended(); return processes.end(targets) },
  }
}

const ended = async (id: string) => (await processes.phase('w-1', id))?.kind === 'exited'

test('silence short of the fence, then contact, leaves Work processes running', async () => {
  const id = await running()
  const value = await fence()
  clock += FENCE_AFTER_MS - 1
  tick()
  await value.heard()
  clock += FENCE_AFTER_MS - 1
  await value.heard()
  assert.equal((await processes.phase('w-1', id))?.kind, 'running')
  assert.deepEqual(logs, [])
})

test('contact right after a sleep past the fence still ends every Work process first', async () => {
  const id = await running()
  const value = await fence()
  // The machine slept: no check ran while the wall clock moved 10 minutes.
  clock += 10 * MINUTE
  await value.heard()
  await until(() => ended(id))
  assert.deepEqual(await processes.phase('w-1', id), { kind: 'exited', run: 1, exitCode: -1 })
  assert.deepEqual(logs, ['fenced: ended 1 Work processes after 600s without the service'])
  const status = await service.request({ workId: 'w-1', method: 'GET', path: `/pty/${id}`, token: 'tok' })
  assert.deepEqual(status.body, { session_id: id, running: false, output_offset: 0 }, 'the process stays known, ended')
  clock += FENCE_AFTER_MS - 1
  tick()
  await value.heard()
  assert.equal(logs.length, 1, 'contact resumed; the fence waits for a new silence')
})

test('a periodic check past the fence ends every Work process, once per silence', async () => {
  const id = await running()
  let ends = 0
  await fence({ processes: counting(() => { ends += 1 }) })
  clock += FENCE_AFTER_MS
  tick()
  await until(() => ended(id))
  await until(async () => logs.length === 1)
  clock += 10 * MINUTE
  tick()
  assert.equal(ends, 1)
})

test('the last contact survives a restart, so a restart while offline fences on time', async () => {
  const id = await running()
  const first = await fence()
  clock += MINUTE
  await first.heard()
  const heardAt = clock
  clock += 5_000
  await first.heard()
  await first.stop()
  assert.deepEqual(JSON.parse(await readFile(join(dir, 'state', 'fence.json'), 'utf8')), { last_heard_at: heardAt + 5_000 })
  clock = heardAt + 5_000 + FENCE_AFTER_MS
  await fence()
  await until(() => ended(id))
  assert.match(logs[0], /^fenced: ended 1 Work processes after 270s/)
})

test('a last contact recorded in the future counts from now', async () => {
  const value = await fence()
  await value.heard()
  await value.stop()
  clock -= MINUTE
  let ends = 0
  await fence({ processes: counting(() => { ends += 1 }) })
  clock += FENCE_AFTER_MS - 1
  tick()
  assert.equal(ends, 0)
})

test('a failing end is retried for the processes it fenced only, not one started after contact resumed', async () => {
  const fenced = await running()
  let attempts = 0
  const value = await fence({
    processes: {
      holdStarts: () => processes.holdStarts(),
      running: () => processes.running(),
      end: (targets) => {
        attempts += 1
        return attempts === 1 ? Promise.reject(new Error('disk gone')) : processes.end(targets)
      },
    },
  })
  clock += 10 * MINUTE
  await value.heard()
  assert.deepEqual(logs, ['fencing failed, retrying on the next check: disk gone'])
  assert.equal((await processes.phase('w-1', fenced))?.kind, 'running')
  // Contact resumed and the platform started another process.
  const later = await running()
  tick()
  await until(() => ended(fenced))
  await until(async () => logs.length === 2)
  assert.equal(attempts, 2)
  assert.equal((await processes.phase('w-1', later))?.kind, 'running', 'the retry ends only what the fence listed')
})

test('a start the contact that fires the fence asks for waits for the fence and survives it', async () => {
  const fenced = await running()
  let listed: RunningProcess[] = []
  const value = await fence({
    processes: {
      holdStarts: () => processes.holdStarts(),
      running: async () => { listed = await processes.running(); return listed },
      end: (targets) => processes.end(targets),
    },
  })
  clock += 10 * MINUTE
  const fencing = value.heard()
  // The frame that ended the silence asks for a start right after.
  const later = running()
  await fencing
  const id = await later
  await until(() => ended(fenced))
  assert.deepEqual(listed.map((target) => target.id), [fenced])
  assert.equal((await processes.phase('w-1', id))?.kind, 'running')
})

test('end ends the listed runs that still run and leaves the rest', async () => {
  const put = await service.request({ workId: 'w-2', method: 'PUT', path: '/', token: '', body: { access_token: 'tok' } })
  assert.equal(put.status, 200)
  const first = await running()
  const response = await service.request({ workId: 'w-2', method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', command: 'true' } })
  const reserved = (response.body as { session_id: string }).session_id
  const listed = await processes.running()
  assert.deepEqual(listed, [{ workId: 'w-1', id: first, run: 1 }])
  const stale = { workId: 'w-1', id: first, run: 2 }
  assert.deepEqual(await processes.end([stale]), { ended: 0, remaining: [] }, 'another run is not the target')
  assert.deepEqual(await processes.end(listed), { ended: 1, remaining: [] })
  await until(() => ended(first))
  assert.deepEqual(await processes.phase('w-2', reserved), { kind: 'reserved' })
  assert.deepEqual(await new ProcessTable({ stateRoot: join(dir, 'nothing'), shim: [] }).running(), [])
})

test('what running runs use is what each recorded at its start, until it ends', async () => {
  assert.deepEqual(await processes.inUse(), new Set())
  const id = await running()
  // A later Talgent Computer, giving its runs another Claude Code.
  const later = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [], uses: [join(dir, 'claude-code', '2.0.0')] })
  assert.deepEqual(await later.inUse(), new Set([join(dir, 'claude-code', '1.0.0')]))
  assert.deepEqual(await processes.end(await processes.running()), { ended: 1, remaining: [] })
  await until(() => ended(id))
  assert.deepEqual(await later.inUse(), new Set())
})
