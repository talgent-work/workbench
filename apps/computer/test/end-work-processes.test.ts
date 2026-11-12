import assert from 'node:assert/strict'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, test } from 'node:test'
import type { PipeSink } from '../src/execd/pipe.ts'
import { WorkInputs } from '../src/execd/inputs.ts'
import { currentHost, type ProcessHost } from '../src/execd/host.ts'
import { ProcessTable, type RunningProcess } from '../src/execd/processes.ts'
import { ExecdService } from '../src/execd/service.ts'
import { SHIM_ARG } from '../src/execd/shim.ts'
import { Works } from '../src/execd/works.ts'
import { CredentialStore } from '../src/proxy/credentials.ts'
import { endEveryWorkProcess } from '../src/end-work-processes.ts'
import { posixOnly } from './support/platform.ts'

const MAIN = fileURLToPath(new URL('../src/main.ts', import.meta.url))

let dir: string
let service: ExecdService
let processes: ProcessTable
const created: string[] = []

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'removal-'))
  processes = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [process.execPath, MAIN, SHIM_ARG] })
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
  for (const work of ['w-1', 'w-2']) {
    const put = await service.request({ workId: work, method: 'PUT', path: '/', token: '', body: { access_token: 'tok' } })
    assert.equal(put.status, 200)
  }
})
afterEach(async () => {
  for (const entry of created.splice(0)) {
    const [work, id] = entry.split('/')
    await processes.delete(work, id)
  }
  // On Windows an ended process tree can hold its Work directory for a moment
  // after it is recorded as exited, so removal retries a bounded few times.
  await rm(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
})

// A Work process that runs until it is ended: it waits for stdin.
async function running(work: string, command = 'read x'): Promise<string> {
  const response = await service.request({ workId: work, method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', command } })
  assert.equal(response.status, 201)
  const id = (response.body as { session_id: string }).session_id
  created.push(`${work}/${id}`)
  const closed = new AbortController()
  const out: PipeSink = { closed: closed.signal, text: () => {}, binary: () => {}, end: () => {}, writable: async () => {} }
  const pipe = await service.openStream({ workId: work, processId: id, since: 0, token: 'tok' }, out)
  assert.ok(!('status' in pipe))
  const deadline = Date.now() + 10_000
  while ((await processes.phase(work, id))?.kind !== 'running') {
    if (Date.now() > deadline) throw new Error('timed out')
    await new Promise((resolve) => setTimeout(resolve, 20))
  }
  closed.abort()
  return id
}

test('a removed Computer ends the processes of every Work on it', async () => {
  const first = await running('w-1')
  const second = await running('w-2')
  const logs: string[] = []
  assert.equal(await endEveryWorkProcess(processes, 'computer_removed', (line) => logs.push(line)), 0)
  assert.deepEqual(await processes.phase('w-1', first), { kind: 'exited', run: 1, exitCode: -1 })
  assert.deepEqual(await processes.phase('w-2', second), { kind: 'exited', run: 1, exitCode: -1 })
  assert.deepEqual(logs, ['ended 2 Work processes: this Computer was removed'])
})

test('processes that cannot be ended are tried again, then reported', async () => {
  const id = await running('w-1')
  let attempts = 0
  const stubborn = {
    holdStarts: () => processes.holdStarts(),
    running: () => processes.running(),
    end: async (targets: RunningProcess[]) => {
      attempts += 1
      return { ended: 0, remaining: targets, failure: new Error('permission denied') }
    },
  } as unknown as ProcessTable
  const logs: string[] = []
  assert.equal(await endEveryWorkProcess(stubborn, 'credential_rejected', (line) => logs.push(line)), 1)
  assert.equal(attempts, 3)
  assert.match(logs[0], /could not end 1 Work processes \(Talgent refuses this Computer's credential\): permission denied/)
  assert.equal((await processes.phase('w-1', id))?.kind, 'running')
})

test('a credential refused for good ends every Work process too', async () => {
  const id = await running('w-1')
  const logs: string[] = []
  assert.equal(await endEveryWorkProcess(processes, 'credential_rejected', (line) => logs.push(line)), 0)
  assert.deepEqual(await processes.phase('w-1', id), { kind: 'exited', run: 1, exitCode: -1 })
  assert.deepEqual(logs, ['ended 1 Work processes: Talgent refuses this Computer\'s credential'])
})

// The fence and the end of every Work process may hold starts at once: a
// start waits until both released their holds.
test('holds on starts nest', async () => {
  const fence = await processes.holdStarts()
  const ending = await processes.holdStarts()
  let started = false
  const later = running('w-1').then((id) => { started = true; return id })
  ending()
  ending()
  await new Promise((resolve) => setTimeout(resolve, 300))
  assert.equal(started, false, 'the first hold is still taken; releasing the later one twice counts once')
  fence()
  const id = await later
  assert.equal((await processes.phase('w-1', id))?.kind, 'running')
})

// Ending a process can end its command before its shim (taskkill /T walks the
// tree in no set order), and the shim then records the command's own exit
// code. An ended process is still recorded as killed.
test('a process whose command ended before its shim is recorded as killed', { skip: posixOnly('stands in for taskkill with POSIX signals') }, async () => {
  const id = await running('w-1', 'trap "exit 1" TERM; while :; do sleep 0.05; done')
  const host = currentHost()
  const commandFirst: ProcessHost = Object.assign(Object.create(Object.getPrototypeOf(host)) as ProcessHost, host, {
    kill: async (started: Parameters<ProcessHost['kill']>[0], stateDir: string) => {
      process.kill(started.pid, 'SIGTERM')
      const deadline = Date.now() + 5_000
      while (Date.now() < deadline && (await host.shimAlive(started, stateDir))) await new Promise((resolve) => setTimeout(resolve, 20))
      await host.kill(started, stateDir)
    },
  })
  const table = new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [process.execPath, MAIN, SHIM_ARG], host: commandFirst })
  const [target] = await table.running()
  assert.equal((await table.end([target])).ended, 1)
  assert.deepEqual(await processes.phase('w-1', id), { kind: 'exited', run: 1, exitCode: -1 })
})
