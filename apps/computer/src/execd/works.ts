import { createHash, timingSafeEqual } from 'node:crypto'
import { chmod, mkdir, readdir, readFile, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { isErrno } from '../store.ts'
import { writeJsonAtomic } from './shim.ts'
import { InvalidTelemetryError, parseTelemetry, type WorkTelemetry } from './telemetry.ts'

// A Work's environment on this machine: its root directory, the execd access
// token every call of the Work presents (kept as a hash), the variables
// every process of the Work gets, and whether its model is a platform Model
// Configuration its processes reach only through the local proxy, whether it
// calls the platform's native API through the local proxy, and where its
// Claude Code sends its telemetry. It lives as long as the Work does.
export interface WorkEnvironment {
  root: string
  environment: Record<string, string>
  modelProxy: boolean
  platformApi: boolean
  telemetry?: WorkTelemetry
}

interface StoredWork {
  tokenSha256: string
  environment: Record<string, string>
  modelProxy: boolean
  // Absent from the records of a Talgent Computer upgraded in place while
  // its Works live, which predates platform_api; the Work's next environment
  // PUT writes it.
  platformApi?: boolean
  // Kept as sent: every process of the Work is given the token. Absent from
  // a platform that sends none.
  telemetry?: WorkTelemetry
}

export type Authorization = { kind: 'ok'; work: WorkEnvironment } | { kind: 'unknown' } | { kind: 'denied' }

// Ids the platform gives Works and processes; anything else could leave the
// directories they name.
export function validId(id: string): boolean {
  return /^[A-Za-z0-9_-]{1,128}$/.test(id)
}

export class InvalidEnvironmentError extends Error {}

export class Works {
  private readonly worksDir: string
  private readonly recordsDir: string
  private readonly cache = new Map<string, StoredWork>()

  // Work roots live under worksDir; their records under recordsDir, outside
  // anything a Work's processes are pointed at.
  constructor(options: { worksDir: string; recordsDir: string }) {
    this.worksDir = options.worksDir
    this.recordsDir = options.recordsDir
  }

  root(workId: string): string {
    return join(this.worksDir, workId)
  }

  // Opens or updates the Work's environment and answers its root.
  async put(workId: string, body: unknown): Promise<string> {
    const { accessToken, environment, modelProxy, platformApi, telemetry } = parseEnvironment(body)
    const root = this.root(workId)
    await mkdir(root, { recursive: true, mode: 0o700 })
    await chmod(root, 0o700)
    await mkdir(this.recordsDir, { recursive: true, mode: 0o700 })
    const stored: StoredWork = { tokenSha256: sha256(accessToken), environment, modelProxy, platformApi, telemetry }
    writeJsonAtomic(join(this.recordsDir, `${workId}.json`), stored)
    this.cache.set(workId, stored)
    return root
  }

  // Whether the Work's environment was opened on this machine.
  async known(workId: string): Promise<boolean> {
    return validId(workId) && (await this.load(workId)) !== undefined
  }

  // Whether the Work's model is reached only through the local proxy.
  async modelProxy(workId: string): Promise<boolean> {
    return validId(workId) && (await this.load(workId))?.modelProxy === true
  }

  // The Works whose environment was opened on this machine.
  async list(): Promise<string[]> {
    let names: string[]
    try {
      names = await readdir(this.recordsDir)
    } catch (error) {
      if (isErrno(error, 'ENOENT')) return []
      throw error
    }
    return names.filter((name) => name.endsWith('.json')).map((name) => name.slice(0, -'.json'.length)).filter(validId)
  }

  // Removes the Work's root with every file in it, then its record, so a
  // removal cut short is found and finished by the next one.
  async remove(workId: string): Promise<void> {
    if (!validId(workId)) return
    await removeTree(this.root(workId))
    await rm(join(this.recordsDir, `${workId}.json`), { force: true })
    this.cache.delete(workId)
  }

  async authorize(workId: string, token: string): Promise<Authorization> {
    if (!validId(workId)) return { kind: 'unknown' }
    const stored = await this.load(workId)
    if (stored === undefined) return { kind: 'unknown' }
    const expected = Buffer.from(stored.tokenSha256, 'hex')
    const presented = Buffer.from(sha256(token), 'hex')
    if (token === '' || !timingSafeEqual(expected, presented)) return { kind: 'denied' }
    const work: WorkEnvironment = { root: this.root(workId), environment: stored.environment, modelProxy: stored.modelProxy, platformApi: stored.platformApi === true }
    if (stored.telemetry !== undefined) work.telemetry = stored.telemetry
    return { kind: 'ok', work }
  }

  private async load(workId: string): Promise<StoredWork | undefined> {
    const cached = this.cache.get(workId)
    if (cached !== undefined) return cached
    let text: string
    try {
      text = await readFile(join(this.recordsDir, `${workId}.json`), 'utf8')
    } catch (error) {
      if (isErrno(error, 'ENOENT')) return undefined
      throw error
    }
    const stored = JSON.parse(text) as StoredWork
    this.cache.set(workId, stored)
    return stored
  }
}

function parseEnvironment(body: unknown): { accessToken: string; environment: Record<string, string>; modelProxy: boolean; platformApi: boolean; telemetry?: WorkTelemetry } {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) throw new InvalidEnvironmentError('body must be an object')
  const { access_token: accessToken, environment = {}, model_proxy: modelProxy = false, platform_api: platformApi = false, telemetry: rawTelemetry } = body as Record<string, unknown>
  if (typeof accessToken !== 'string' || accessToken.trim() === '') throw new InvalidEnvironmentError('access_token required')
  if (typeof modelProxy !== 'boolean') throw new InvalidEnvironmentError('model_proxy must be a boolean')
  if (typeof platformApi !== 'boolean') throw new InvalidEnvironmentError('platform_api must be a boolean')
  if (typeof environment !== 'object' || environment === null || Array.isArray(environment)) {
    throw new InvalidEnvironmentError('environment must be an object')
  }
  const result: Record<string, string> = {}
  for (const [name, value] of Object.entries(environment)) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || typeof value !== 'string' || value.includes('\0')) {
      throw new InvalidEnvironmentError(`invalid environment variable ${JSON.stringify(name)}`)
    }
    result[name] = value
  }
  let telemetry: WorkTelemetry | undefined
  try {
    telemetry = parseTelemetry(rawTelemetry)
  } catch (error) {
    if (error instanceof InvalidTelemetryError) throw new InvalidEnvironmentError(error.message)
    throw error
  }
  return { accessToken, environment: result, modelProxy, platformApi, telemetry }
}

// Removes a directory tree, read-only parts included: the Work's inputs
// directory is left without write permission, which a plain removal of its
// files meets on POSIX.
export async function removeTree(path: string): Promise<void> {
  try {
    await rm(path, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
  } catch (error) {
    if (!isErrno(error, 'EACCES') && !isErrno(error, 'EPERM')) throw error
    await openTree(path)
    await rm(path, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
  }
}

// Gives this user write permission on every directory of a tree it owns.
async function openTree(dir: string): Promise<void> {
  let entries
  try {
    await chmod(dir, 0o700)
    entries = await readdir(dir, { withFileTypes: true })
  } catch (error) {
    if (isErrno(error, 'ENOENT') || isErrno(error, 'ENOTDIR')) return
    throw error
  }
  for (const entry of entries) if (entry.isDirectory()) await openTree(join(dir, entry.name))
}

function sha256(value: string): string {
  return createHash('sha256').update(value).digest('hex')
}
