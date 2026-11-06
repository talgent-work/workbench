import { randomBytes } from 'node:crypto'
import { chmod, link, mkdir, open, readFile, rename, rm } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { isObject } from './json.ts'
import type { Paths } from './paths.ts'

// The configuration file a person may write: the Talgent server register
// uses when --server is not given.
export interface Config {
  server?: string
}

// What registration leaves on the machine. The credential authenticates this
// Computer and is issued once, so it is never printed.
export interface Credential {
  server: string
  computerId: string
  name: string
  credential: string
  // ISO 8601, or null when the server set no expiry.
  credentialExpiresAt: string | null
}

export class CredentialExistsError extends Error {
  readonly path: string
  constructor(path: string) {
    super(`a credential already exists at ${path}`)
    this.path = path
  }
}

export async function readConfig(paths: Paths): Promise<Config> {
  const raw = await readJson(paths.configFile)
  if (raw === undefined) return {}
  if (!isObject(raw)) throw new Error(`${paths.configFile} is not a JSON object`)
  return { server: typeof raw.server === 'string' ? raw.server : undefined }
}

export async function readCredential(paths: Paths): Promise<Credential | undefined> {
  const raw = await readJson(paths.credentialFile)
  if (raw === undefined) return undefined
  if (
    !isObject(raw) ||
    typeof raw.server !== 'string' ||
    typeof raw.computerId !== 'string' ||
    typeof raw.name !== 'string' ||
    typeof raw.credential !== 'string' ||
    !(raw.credentialExpiresAt === null || typeof raw.credentialExpiresAt === 'string')
  ) {
    throw new Error(`${paths.credentialFile} is not a valid credential file`)
  }
  return {
    server: raw.server,
    computerId: raw.computerId,
    name: raw.name,
    credential: raw.credential,
    credentialExpiresAt: raw.credentialExpiresAt,
  }
}

// Writes the credential readable only by its owner. The file is written in
// full and flushed to disk under a temporary name first, so a crash never
// leaves half a credential behind. Without force an existing credential is
// kept: the final name is hard-linked, which fails atomically when it already
// exists; with force the final name is replaced atomically. The directory is
// flushed after, where the platform supports it, so the new name survives a
// crash too.
export async function writeCredential(paths: Paths, credential: Credential, options: { force: boolean }): Promise<void> {
  await mkdir(paths.configDir, { recursive: true, mode: 0o700 })
  await chmod(paths.configDir, 0o700)
  const tmp = join(dirname(paths.credentialFile), `.credential.${randomBytes(6).toString('hex')}.tmp`)
  try {
    const file = await open(tmp, 'wx', 0o600)
    try {
      await file.writeFile(JSON.stringify(credential, null, 2) + '\n')
      await file.sync()
    } finally {
      await file.close()
    }
    if (options.force) {
      await rename(tmp, paths.credentialFile)
    } else {
      try {
        await link(tmp, paths.credentialFile)
      } catch (error) {
        if (isErrno(error, 'EEXIST')) throw new CredentialExistsError(paths.credentialFile)
        throw error
      }
    }
    await syncDirectory(dirname(paths.credentialFile))
  } finally {
    await rm(tmp, { force: true })
  }
}

// Flushes a directory's entries to disk. Windows opens no directory for
// this, and some file systems refuse it; there the rename stands as the
// file system keeps it.
async function syncDirectory(dir: string): Promise<void> {
  if (process.platform === 'win32') return
  const handle = await open(dir, 'r')
  try {
    await handle.sync()
  } catch (error) {
    if (!UNSUPPORTED_SYNC.some((code) => isErrno(error, code))) throw error
  } finally {
    await handle.close()
  }
}

const UNSUPPORTED_SYNC = ['EINVAL', 'ENOTSUP', 'EISDIR', 'EPERM']

async function readJson(path: string): Promise<unknown> {
  let text: string
  try {
    text = await readFile(path, 'utf8')
  } catch (error) {
    if (isErrno(error, 'ENOENT')) return undefined
    throw error
  }
  try {
    return JSON.parse(text)
  } catch {
    throw new Error(`${path} is not valid JSON`)
  }
}

export function isErrno(error: unknown, code: string): boolean {
  return error instanceof Error && (error as NodeJS.ErrnoException).code === code
}
