import assert from 'node:assert/strict'
import { mkdtemp, readdir, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { CredentialStore } from '../src/proxy/credentials.ts'

const IDENTITY_TOKEN = 'agent-identity-token'

let dir: string
beforeEach(async () => { dir = await mkdtemp(join(tmpdir(), 'credentials-')) })
afterEach(() => rm(dir, { recursive: true, force: true }))

const body = (key: string) => ({
  model: { base_url: 'https://vendor.example/anthropic', key },
  gateway: { url: 'https://talgent.example/computer/v1/mcp', identity_token: IDENTITY_TOKEN },
})

test('concurrent puts and deletes of a Work apply in call order; file and cache agree', async () => {
  const store = new CredentialStore({ dir })
  const calls: Promise<void>[] = []
  for (let i = 0; i < 20; i++) {
    calls.push(store.put('w-1', body(`key-${i}`)))
    if (i === 10) calls.push(store.delete('w-1'))
  }
  const reads = Array.from({ length: 5 }, () => store.get('w-1'))
  await Promise.all([...calls, ...reads])
  assert.equal((await store.get('w-1'))?.model?.key, 'key-19')
  const fresh = new CredentialStore({ dir })
  assert.equal((await fresh.get('w-1'))?.model?.key, 'key-19', 'the file holds the last put')
  assert.deepEqual(JSON.parse(await readFile(join(dir, 'w-1.json'), 'utf8')).model.key, 'key-19')
  assert.deepEqual(await readdir(dir), ['w-1.json'], 'no temporary files are left')

  await Promise.all([store.put('w-1', body('late')), store.delete('w-1')])
  assert.equal(await store.get('w-1'), undefined, 'the delete came last')
  assert.equal(await new CredentialStore({ dir }).get('w-1'), undefined)
})

test('a put that fails validation changes nothing', async () => {
  const store = new CredentialStore({ dir })
  await store.put('w-1', body('kept'))
  await assert.rejects(store.put('w-1', { gateway: {} }))
  assert.equal((await store.get('w-1'))?.model?.key, 'kept')
})

test('a redelivery that keeps the model takes the new Identity Token and keeps the held key', async () => {
  const store = new CredentialStore({ dir })
  await store.put('w-1', body('held-key'))
  const { model: _model, ...withoutModel } = body('unused')
  void _model
  const renewed = { ...withoutModel, gateway: { ...withoutModel.gateway, identity_token: 'renewed-token' }, keep_model: true }
  await store.put('w-1', renewed)
  for (const reader of [store, new CredentialStore({ dir })]) {
    const held = await reader.get('w-1')
    assert.equal(held?.model?.key, 'held-key')
    assert.equal(held?.gateway.identityToken, 'renewed-token')
  }
  await assert.rejects(store.put('w-1', { ...body('x'), keep_model: true }), /keep_model excludes model/)
})
