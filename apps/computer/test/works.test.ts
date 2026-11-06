import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { InvalidEnvironmentError, validId, Works } from '../src/execd/works.ts'
import { posixModes } from './support/platform.ts'

let dir: string
let works: Works
beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'works-'))
  works = new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') })
})
afterEach(() => rm(dir, { recursive: true, force: true }))

test('opening an environment creates a private Work root and stores only a token hash', async () => {
  const root = await works.put('w-1', { access_token: 'secret-token', environment: { A: '1' } })
  assert.equal(root, join(dir, 'works', 'w-1'))
  if (posixModes) assert.equal((await stat(root)).mode & 0o777, 0o700)
  const record = await readFile(join(dir, 'state', 'works', 'w-1.json'), 'utf8')
  assert.ok(!record.includes('secret-token'))
  if (posixModes) assert.equal((await stat(join(dir, 'state', 'works', 'w-1.json'))).mode & 0o777, 0o600)
  assert.deepEqual(await works.authorize('w-1', 'secret-token'), { kind: 'ok', work: { root, environment: { A: '1' }, modelProxy: false, platformApi: false } })
})

test('every call must present the Work token; unknown Works are unknown', async () => {
  await works.put('w-1', { access_token: 'secret-token' })
  assert.deepEqual(await works.authorize('w-1', 'other'), { kind: 'denied' })
  assert.deepEqual(await works.authorize('w-1', ''), { kind: 'denied' })
  assert.deepEqual(await works.authorize('w-2', 'secret-token'), { kind: 'unknown' })
  assert.deepEqual(await works.authorize('../w-1', 'secret-token'), { kind: 'unknown' })
})

test('model_proxy marks a Work on a Model Configuration, and survives a restart', async () => {
  await works.put('w-1', { access_token: 't', model_proxy: true })
  await works.put('w-2', { access_token: 't' })
  const reloaded = new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') })
  assert.equal(await reloaded.modelProxy('w-1'), true)
  assert.equal(await reloaded.modelProxy('w-2'), false)
  assert.equal(await reloaded.modelProxy('w-3'), false)
  const access = await reloaded.authorize('w-1', 't')
  assert.equal(access.kind === 'ok' && access.work.modelProxy, true)
  await works.put('w-1', { access_token: 't' })
  assert.equal(await works.modelProxy('w-1'), false, 'repeating the PUT without it withdraws it')
})

test('repeating the PUT replaces token and environment, and both survive a restart', async () => {
  await works.put('w-1', { access_token: 'one', environment: { A: '1' } })
  await works.put('w-1', { access_token: 'two', environment: { B: '2' } })
  const reloaded = new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') })
  assert.deepEqual(await reloaded.authorize('w-1', 'one'), { kind: 'denied' })
  const access = await reloaded.authorize('w-1', 'two')
  assert.equal(access.kind, 'ok')
  assert.deepEqual(access.kind === 'ok' && access.work.environment, { B: '2' })
})

test('the telemetry the platform names is kept with the Work, survives a restart and is withdrawn by a PUT without it', async () => {
  const telemetry = { endpoint: 'https://workbench.example/computer/v1/otel', token: 'TELEMETRYTOKEN234567' }
  await works.put('w-1', { access_token: 't', telemetry })
  const reloaded = new Works({ worksDir: join(dir, 'works'), recordsDir: join(dir, 'state', 'works') })
  const access = await reloaded.authorize('w-1', 't')
  assert.deepEqual(access.kind === 'ok' && access.work.telemetry, telemetry)
  await works.put('w-1', { access_token: 't' })
  const withdrawn = await works.authorize('w-1', 't')
  assert.equal(withdrawn.kind === 'ok' && withdrawn.work.telemetry, undefined)
})

test('an invalid environment body is rejected', async () => {
  const token = 'TELEMETRYTOKEN234567'
  const invalidTelemetry = [
    null, 'x', { token }, { endpoint: 'http://workbench.example/otel', token }, { endpoint: 'https://u:p@workbench.example/otel', token },
    { endpoint: 'https://workbench.example/otel?q=1', token }, { endpoint: 'not a url', token },
    { endpoint: 'https://workbench.example/otel', token: 'short' }, { endpoint: 'https://workbench.example/otel', token: `${token},X-Other=1` },
  ].map((telemetry) => ({ access_token: 't', telemetry }))
  for (const body of [null, [], {}, { access_token: ' ' }, { access_token: 't', environment: [] }, { access_token: 't', environment: { 'A=B': 'x' } }, { access_token: 't', environment: { A: 1 } }, { access_token: 't', model_proxy: 'yes' }, { access_token: 't', model_proxy: null }, ...invalidTelemetry]) {
    await assert.rejects(works.put('w-1', body), InvalidEnvironmentError, JSON.stringify(body))
  }
})

test('ids are path-safe', () => {
  assert.ok(validId('0f3c-AB_9'))
  for (const id of ['', '..', 'a/b', 'a.b', 'x'.repeat(129)]) assert.equal(validId(id), false, id)
})

test('a Work root may sit under a home with spaces', async () => {
  const spaced = new Works({ worksDir: join(dir, 'John Smith', 'works'), recordsDir: join(dir, 'state', 'works') })
  assert.equal(await spaced.put('w-1', { access_token: 't' }), join(dir, 'John Smith', 'works', 'w-1'))
})
