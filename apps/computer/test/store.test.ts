import assert from 'node:assert/strict'
import { mkdtemp, readdir, rm, stat, writeFile, mkdir } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { resolvePaths, type Paths } from '../src/paths.ts'
import { CredentialExistsError, readConfig, readCredential, writeCredential, type Credential } from '../src/store.ts'

let root: string
let paths: Paths

const credential: Credential = {
  server: 'https://talgent.example',
  computerId: 'c-1',
  name: 'laptop',
  credential: 'secret-1',
  credentialExpiresAt: '2027-01-01T00:00:00.000Z',
}

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), 'talgent-computer-'))
  paths = resolvePaths({ XDG_CONFIG_HOME: join(root, 'config') }, root)
})
afterEach(() => rm(root, { recursive: true, force: true }))

// File modes are POSIX; on Windows the per-user profile folders hold the files.
const posixModes = process.platform !== 'win32'

test('paths follow XDG_CONFIG_HOME and fall back to ~/.config', () => {
  assert.equal(paths.credentialFile, join(root, 'config', 'talgent-computer', 'credential.json'))
  assert.deepEqual(resolvePaths({}, '/home/u', 'linux'), {
    configDir: '/home/u/.config/talgent-computer',
    configFile: '/home/u/.config/talgent-computer/config.json',
    credentialFile: '/home/u/.config/talgent-computer/credential.json',
    dataDir: '/home/u/.local/share/talgent-computer',
  })
  assert.equal(resolvePaths({}, '/Users/u', 'darwin').dataDir, '/Users/u/.local/share/talgent-computer')
})

test('on Windows, configuration roams in APPDATA and program data stays in LOCALAPPDATA', () => {
  const env = { APPDATA: 'C:\\Users\\u\\AppData\\Roaming', LOCALAPPDATA: 'D:\\Local' }
  assert.deepEqual(resolvePaths(env, 'C:\\Users\\u', 'win32'), {
    configDir: 'C:\\Users\\u\\AppData\\Roaming\\talgent-computer',
    configFile: 'C:\\Users\\u\\AppData\\Roaming\\talgent-computer\\config.json',
    credentialFile: 'C:\\Users\\u\\AppData\\Roaming\\talgent-computer\\credential.json',
    dataDir: 'D:\\Local\\talgent-computer',
  })
  assert.equal(resolvePaths({}, 'C:\\Users\\u', 'win32').dataDir, 'C:\\Users\\u\\AppData\\Local\\talgent-computer')
  assert.equal(resolvePaths({ ...env, XDG_DATA_HOME: 'E:\\xdg' }, 'C:\\Users\\u', 'win32').dataDir, 'E:\\xdg\\talgent-computer')
})

test('writes the credential owner-only and reads it back', async () => {
  await writeCredential(paths, credential, { force: false })
  if (posixModes) {
    assert.equal((await stat(paths.credentialFile)).mode & 0o777, 0o600)
    assert.equal((await stat(paths.configDir)).mode & 0o777, 0o700)
  }
  assert.deepEqual(await readCredential(paths), credential)
  assert.deepEqual(await readdir(paths.configDir), ['credential.json'])
})

test('refuses to overwrite an existing credential without force', async () => {
  await writeCredential(paths, credential, { force: false })
  await assert.rejects(
    writeCredential(paths, { ...credential, credential: 'secret-2' }, { force: false }),
    CredentialExistsError,
  )
  assert.equal((await readCredential(paths))?.credential, 'secret-1')
  assert.deepEqual(await readdir(paths.configDir), ['credential.json'])
})

test('force replaces the credential', async () => {
  await writeCredential(paths, credential, { force: false })
  await writeCredential(paths, { ...credential, credential: 'secret-2' }, { force: true })
  assert.equal((await readCredential(paths))?.credential, 'secret-2')
  if (posixModes) assert.equal((await stat(paths.credentialFile)).mode & 0o777, 0o600)
})

test('missing files read as empty; config server is read', async () => {
  assert.equal(await readCredential(paths), undefined)
  assert.deepEqual(await readConfig(paths), {})
  await mkdir(paths.configDir, { recursive: true })
  await writeFile(paths.configFile, '{"server":"https://talgent.example"}')
  assert.deepEqual(await readConfig(paths), { server: 'https://talgent.example' })
})
