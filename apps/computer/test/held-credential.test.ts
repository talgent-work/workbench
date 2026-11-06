import assert from 'node:assert/strict'
import { mkdtemp, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { HeldCredential } from '../src/held-credential.ts'
import { resolvePaths, type Paths } from '../src/paths.ts'
import { readCredential, writeCredential, type Credential } from '../src/store.ts'

let root: string
let paths: Paths

const registered: Credential = {
  server: 'https://talgent.example',
  computerId: 'c-1',
  name: 'laptop',
  credential: 'tc_old',
  credentialExpiresAt: '2026-12-01T00:00:00.000Z',
}

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), 'held-credential-'))
  paths = resolvePaths({ XDG_CONFIG_HOME: join(root, 'config') }, root)
  await writeCredential(paths, registered, { force: false })
})
afterEach(() => rm(root, { recursive: true, force: true }))

test('a rotated credential is presented once it is saved in full', async () => {
  const held = new HeldCredential(paths, registered)
  assert.equal(held.secret(), 'tc_old')
  await held.replace({ credential: 'tc_next', expiresAt: '2027-01-01T00:00:00.000Z' })
  assert.equal(held.secret(), 'tc_next')
  assert.deepEqual(await readCredential(paths), { ...registered, credential: 'tc_next', credentialExpiresAt: '2027-01-01T00:00:00.000Z' })
  assert.deepEqual(await readdir(paths.configDir), ['credential.json'], 'no temporary file is left behind')
})

test('a credential that could not be saved is not presented', async () => {
  // The configuration directory sits under a file, so nothing is written.
  const blocker = join(root, 'blocker')
  await writeFile(blocker, '')
  const unwritable: Paths = { ...paths, configDir: join(blocker, 'config'), credentialFile: join(blocker, 'config', 'credential.json') }
  const held = new HeldCredential(unwritable, registered)
  await assert.rejects(held.replace({ credential: 'tc_lost', expiresAt: null }))
  assert.equal(held.secret(), 'tc_old', 'the connection keeps presenting the credential it has')
  assert.equal((await readCredential(paths))?.credential, 'tc_old')
})
