import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises'
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

function serviceOver(over: ProcessTable, platform?: NodeJS.Platform): ExecdService {
  return new ExecdService({
    platform,
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

test('a creation with invalid Personal Variables is refused without naming a value', async () => {
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
  assert.deepEqual(layerPersonalVariables(base, parsePersonalVariables({ path: '/mine', talgent_work_root: '/x' }), 'win32'), { path: '/mine', TALGENT_WORK_ROOT: 'C:\\w' })
  assert.deepEqual(layerPersonalVariables({ PATH: '/bin' }, { path: '/mine' }, 'linux'), { PATH: '/bin', path: '/mine' })
})

test('a Harness process with Personal Variables near their total size launches for real', { skip: portableProcessRules }, async () => {
  // Eight values of 32 KiB, each within the per-value limit, 256 KiB in all:
  // more than one environment string the machine passes to a program may hold.
  const personal = Object.fromEntries(Array.from({ length: 8 }, (_, i) => [`LARGE_${i}`, String.fromCharCode(97 + i).repeat(32 << 10)]))
  const { id } = await create({ command: 'for v in "$LARGE_0" "$LARGE_7"; do printf "%s\\n" "${#v}"; done; printf "%s" "${LARGE_3:0:3}"', personal_variables: personal })
  const { stdout, exit } = await run(id)
  assert.equal(stdout, `${32 << 10}\n${32 << 10}\nddd`)
  assert.equal(exit, '{"type":"exit","exit_code":0}\n')
  const state = processes.dir('w-1', id)
  for (const entry of await readdir(state, { withFileTypes: true })) {
    if (!entry.isFile()) continue
    const file = entry.name
    const content = await readFile(join(state, file), 'latin1')
    for (const value of Object.values(personal)) assert.ok(!content.includes(value.slice(0, 64)), `no value rests in ${file}`)
  }
})

test('the answer names the variables of the Work and of Talgent Computer that the Personal Variables replace', async () => {
  const personal = {
    MACHINE: 'm', A: 'w', HOME: '/h', PATH: '/person/bin', ANTHROPIC_BASE_URL: 'u', ANTHROPIC_AUTH_TOKEN: 't', ANTHROPIC_API_KEY: 'k',
    OTEL_EXPORTER_OTLP_ENDPOINT: 'e', OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: 'e', MINE: 'v', TALGENT_WORK_ROOT: '/x',
  }
  const { answer } = await create({ command: 'true', personal_variables: personal })
  assert.deepEqual(answer.replaced_variables, [
    'A', 'ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL', 'HOME', 'OTEL_EXPORTER_OTLP_ENDPOINT', 'OTEL_EXPORTER_OTLP_TRACES_ENDPOINT', 'PATH',
  ], 'never the machine\'s own, one Talgent Computer does not set or a TALGENT_ one')

  const windows = serviceOver(processes, 'win32')
  const { answer: caseless } = await create({ command: 'true', personal_variables: { path: '/p', home: '/h', a: 'w', machine: 'm' } }, windows)
  assert.deepEqual(caseless.replaced_variables, ['a', 'home', 'path'], 'Windows compares names without case')

  const { answer: none } = await create({ command: 'true', personal_variables: { MINE: 'v' } })
  assert.deepEqual(none.replaced_variables, [])
})

test('a person\'s PATH takes the place of PATH, the bundled Claude Code not put before it', { skip: portableProcessRules }, async () => {
  const { id } = await create({ command: '/bin/echo "$PATH"', personal_variables: { PATH: '/person/bin:/usr/bin:/bin' } })
  assert.equal((await run(id)).stdout, '/person/bin:/usr/bin:/bin\n')
})

test('a Harness reserved before a restart awaits its Personal Variables instead of running or failing', { skip: portableProcessRules }, async () => {
  const { id } = await create({ command: 'echo "$MACHINE"; touch "$TALGENT_WORK_ROOT/ran"', personal_variables: { MACHINE: 'person-machine' } })
  const restarted = serviceOver(table())
  const call = (method: string, path: string, body?: unknown) => restarted.request({ workId: 'w-1', method, path, token: 'tok', body })

  const status = await call('GET', `/pty/${id}`)
  assert.equal((status.body as Record<string, unknown>).awaiting_personal_variables, true)
  const closed = new AbortController()
  const refused = await restarted.openStream({ workId: 'w-1', processId: id, since: 0, token: 'tok' }, {
    closed: closed.signal, text: () => {}, binary: () => {}, end: () => {}, writable: async () => {},
  })
  assert.deepEqual(refused, { status: 412 }, 'the attach is refused, the process left reserved')
  assert.equal(existsSync(join(dir, 'works', 'w-1', 'ran')), false)

  assert.equal((await call('PUT', `/pty/${id}/personal_variables`, { personal_variables: ['A'] })).status, 400)
  assert.equal((await call('PUT', '/pty/unknown/personal_variables', { personal_variables: {} })).status, 404)
  assert.equal((await call('PUT', `/pty/${id}/personal_variables`, { personal_variables: { MACHINE: 'person-machine' } })).status, 204)
  assert.equal('awaiting_personal_variables' in ((await call('GET', `/pty/${id}`)).body as Record<string, unknown>), false)
  assert.equal((await run(id, restarted)).stdout, 'person-machine\n', 'it runs with the variables given again')
  assert.equal((await call('PUT', `/pty/${id}/personal_variables`, { personal_variables: { MACHINE: 'other' } })).status, 409, 'a process that ran takes none')
})
