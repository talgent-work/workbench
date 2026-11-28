import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, test } from 'node:test'
import type { PipeSink } from '../src/execd/pipe.ts'
import { layerPersonalVariables, parsePersonalVariables } from '../src/execd/personal-variables.ts'
import { ProcessTable } from '../src/execd/processes.ts'
import { ExecdService } from '../src/execd/service.ts'
import { SHIM_ARG } from '../src/execd/shim.ts'
import { Works } from '../src/execd/works.ts'
import { CredentialStore } from '../src/proxy/credentials.ts'
import { removeScratch, portableProcessRules } from './support/platform.ts'

// A Work's Harness process gets the run's Personal Variables last, over the
// machine's, the Work's and Talgent Computer's own variables, TALGENT_ ones
// excepted; every other process of the Work keeps today's environment.

const MAIN = fileURLToPath(new URL('../src/main.ts', import.meta.url))
const TOKEN = 'telemetry-token-0123456789'

let dir: string
let processes: ProcessTable
let service: ExecdService
const created: string[] = []

function table(): ProcessTable {
  return new ProcessTable({ stateRoot: join(dir, 'state', 'processes'), shim: [process.execPath, MAIN, SHIM_ARG] })
}

function serviceOver(over: ProcessTable): ExecdService {
  return new ExecdService({
    works: new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') }),
    processes: over,
    claudeBinDir: '/opt/claude-code/bin',
    credentials: new CredentialStore({ dir: join(dir, 'state', 'credentials') }),
    proxyOrigin: () => 'http://127.0.0.1:1',
    baseEnv: { PATH: '/usr/bin:/bin', MACHINE: 'm', HOME: '/ignored' },
    home: '/home/owner',
    log: () => {},
  })
}

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'personal-'))
  processes = table()
  service = serviceOver(processes)
  const put = await service.request({
    workId: 'w-1', method: 'PUT', path: '/', token: '',
    body: { access_token: 'tok', environment: { A: 'from-work' }, model_proxy: true, telemetry: { endpoint: 'https://telemetry.example', token: TOKEN } },
  })
  assert.equal(put.status, 200)
})
afterEach(async () => {
  for (const id of created.splice(0)) await processes.delete('w-1', id)
  await removeScratch(dir)
})

const ECHO = 'echo "$MACHINE|$A|$HOME|$ANTHROPIC_BASE_URL|$OTEL_EXPORTER_OTLP_ENDPOINT|$TALGENT_WORK_ROOT|${talgent_extra-unset}"'

async function create(body: Record<string, unknown>, over = service): Promise<{ id: string; answer: Record<string, unknown> }> {
  const response = await over.request({ workId: 'w-1', method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', ...body } })
  assert.equal(response.status, 201)
  const answer = response.body as Record<string, unknown>
  created.push(answer.session_id as string)
  return { id: answer.session_id as string, answer }
}

// Runs the process to its exit and answers its stdout, stderr and exit frame.
async function run(id: string, over = service): Promise<{ stdout: string; stderr: string; exit: string | undefined }> {
  const closed = new AbortController()
  const texts: string[] = []
  const binaries: Buffer[] = []
  let ended = false
  const sink: PipeSink = { closed: closed.signal, text: (text) => texts.push(text), binary: (frame) => binaries.push(frame), end: () => { ended = true }, writable: async () => {} }
  const pipe = await over.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, sink)
  assert.ok(!('status' in pipe))
  const deadline = Date.now() + 2_500
  while (!ended) {
    if (Date.now() > deadline) throw new Error('timed out')
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  const stream = (n: number) => binaries.filter((frame) => frame[0] === n).map((frame) => frame.subarray(1).toString()).join('')
  return { stdout: stream(1), stderr: stream(2), exit: texts.find((text) => text.includes('"exit"')) }
}

test('the Harness process has its Personal Variables over every other variable, TALGENT_ ones excepted', { skip: portableProcessRules }, async () => {
  const personal = {
    MACHINE: 'person-machine', A: 'person-work', HOME: '/person-home', ANTHROPIC_BASE_URL: 'https://person.example',
    OTEL_EXPORTER_OTLP_ENDPOINT: 'https://person-telemetry.example', TALGENT_WORK_ROOT: '/elsewhere', talgent_extra: 'lowercase',
  }
  const { id, answer } = await create({ command: ECHO, personal_variables: personal })
  assert.equal(answer.personal_variables, true, 'the answer acknowledges the variables')
  const spec = await readFile(join(processes.dir('w-1', id), 'spec.json'), 'utf8')
  for (const value of Object.values(personal)) assert.ok(!spec.includes(value), 'no value rests on disk')

  const root = join(dir, 'works', 'w-1')
  const { stdout, exit } = await run(id)
  assert.equal(stdout, `person-machine|person-work|/person-home|https://person.example|https://person-telemetry.example|${root}|unset\n`)
  assert.equal(exit, '{"type":"exit","exit_code":0}\n')
})

test('a preparation process keeps the environment it had without Personal Variables', { skip: portableProcessRules }, async () => {
  await create({ command: 'true', personal_variables: { MACHINE: 'person-machine' } })
  const { id, answer } = await create({ command: ECHO })
  assert.equal('personal_variables' in answer, false)
  const root = join(dir, 'works', 'w-1')
  const { stdout } = await run(id)
  assert.equal(stdout, `m|from-work|/home/owner|http://127.0.0.1:1/w/w-1/model|https://telemetry.example|${root}|unset\n`)
})

test('a creation with invalid Personal Variables is refused without naming a value', { skip: portableProcessRules }, async () => {
  for (const personal of [['A'], 'A=1', { '1BAD': 'secret-value' }, { A: 7 }, { A: 'secret\0value' }]) {
    const response = await service.request({ workId: 'w-1', method: 'POST', path: '/pty', token: 'tok', body: { cwd: '/workspace', command: 'true', personal_variables: personal } })
    assert.equal(response.status, 400)
    assert.ok(!JSON.stringify(response.body).includes('secret'))
  }
})

test('a run again keeps the Personal Variables; after a restart the Harness never runs without them', { skip: portableProcessRules }, async () => {
  const { id } = await create({ command: 'echo "$MACHINE"; touch "$TALGENT_WORK_ROOT/ran"', personal_variables: { MACHINE: 'person-machine' } })
  assert.equal((await run(id)).stdout, 'person-machine\n')
  assert.equal((await run(id)).stdout, 'person-machine\n', 'a later run of the same process has them again')

  const root = join(dir, 'works', 'w-1')
  const restarted = serviceOver(table())
  await rm(join(root, 'ran'))
  const refused = await run(id, restarted)
  assert.equal(refused.stdout, '')
  assert.match(refused.stderr, /created with Personal Variables/)
  assert.equal(refused.exit, '{"type":"exit","exit_code":-1}\n')
  assert.equal(existsSync(join(root, 'ran')), false, 'the command did not run')
})

test('layering compares names as the machine does and never replaces a TALGENT_ variable', () => {
  const variables = parsePersonalVariables({ path: '/mine', Talgent_Work_Root: '/elsewhere', MULTI: 'a\nb' })
  assert.deepEqual(variables, { path: '/mine', MULTI: 'a\nb' }, 'a reserved name is left out in any case')
  const base = { Path: 'C:\\Windows', TALGENT_WORK_ROOT: 'C:\\w' }
  assert.deepEqual(layerPersonalVariables(base, { path: '/mine', talgent_work_root: '/x' }, 'win32'), { path: '/mine', TALGENT_WORK_ROOT: 'C:\\w' })
  assert.deepEqual(layerPersonalVariables({ PATH: '/bin' }, { path: '/mine' }, 'linux'), { PATH: '/bin', path: '/mine' })
})
