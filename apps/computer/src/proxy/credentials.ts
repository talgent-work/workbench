import { randomBytes } from 'node:crypto'
import { chmod, mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { isObject } from '../json.ts'
import { isErrno } from '../store.ts'

// A Work's credentials, which the platform delivers so the local proxy can
// reach the model vendor and the platform on the Work's behalf: its Identity
// Token goes as `Authorization: Bearer` with the Agent's platform requests.
// They are kept outside every Work root, readable only by this user, and
// never given to a process.
export interface WorkCredentials {
  // Absent for a Work on the machine's own Claude login.
  model?: { baseUrl: string; key: string }
  gateway: { url: string; workId: string; identityToken: string; workCredential: string; spaceToken?: string }
  // Where the Work's native API calls go, with the gateway's identity; absent
  // where Works reach the platform only through the Gateway.
  api?: { url: string }
}

// Its message names the invalid field, never a value.
export class InvalidCredentialsError extends Error {}

export class CredentialStore {
  private readonly dir: string
  // Delivered credentials read or written so far; a delete drops its Work.
  private readonly cache = new Map<string, WorkCredentials>()
  // Per Work, the last pending put or delete, so file and cache change in
  // the order the calls came.
  private readonly pending = new Map<string, Promise<void>>()
  // Counts puts and deletes, so a read that raced one is not cached.
  private changes = 0

  // dir holds one file per Work; it must lie outside every Work root.
  constructor(options: { dir: string }) {
    this.dir = options.dir
  }

  // Validates and stores a Work's credentials, replacing earlier ones. A
  // body that asks to keep the model keeps the one held before, if any.
  async put(workId: string, body: unknown): Promise<void> {
    const { credentials, keepModel } = parseDelivery(body)
    await this.serialize(workId, async () => {
      if (keepModel) {
        const held = await this.read(workId)
        if (held?.model !== undefined) credentials.model = held.model
      }
      await mkdir(this.dir, { recursive: true, mode: 0o700 })
      await chmod(this.dir, 0o700)
      const tmp = join(this.dir, `.${workId}.${randomBytes(6).toString('hex')}.tmp`)
      try {
        await writeFile(tmp, JSON.stringify(credentials), { mode: 0o600, flag: 'wx' })
        await rename(tmp, this.file(workId))
      } finally {
        await rm(tmp, { force: true })
      }
      this.cache.set(workId, credentials)
    })
  }

  delete(workId: string): Promise<void> {
    return this.serialize(workId, async () => {
      await rm(this.file(workId), { force: true })
      this.cache.delete(workId)
    })
  }

  async get(workId: string): Promise<WorkCredentials | undefined> {
    await this.pending.get(workId)?.catch(() => {})
    const changes = this.changes
    const cached = this.cache.get(workId)
    if (cached !== undefined) return cached
    const stored = await this.read(workId)
    // A put or delete that ran meanwhile decides instead.
    if (stored !== undefined && changes === this.changes && !this.pending.has(workId)) this.cache.set(workId, stored)
    return stored
  }

  // The Work's stored credentials, from the cache or its file.
  private async read(workId: string): Promise<WorkCredentials | undefined> {
    const cached = this.cache.get(workId)
    if (cached !== undefined) return cached
    let text: string
    try {
      text = await readFile(this.file(workId), 'utf8')
    } catch (error) {
      if (isErrno(error, 'ENOENT')) return undefined
      throw error
    }
    return JSON.parse(text) as WorkCredentials
  }

  private serialize(workId: string, change: () => Promise<void>): Promise<void> {
    const previous = this.pending.get(workId) ?? Promise.resolve()
    const next = previous.catch(() => {}).then(() => {
      this.changes += 1
      return change()
    })
    this.pending.set(workId, next)
    const settle = () => {
      if (this.pending.get(workId) === next) this.pending.delete(workId)
    }
    next.then(settle, settle)
    return next
  }

  private file(workId: string): string {
    return join(this.dir, `${workId}.json`)
  }
}

// A value that travels in an HTTP header: visible ASCII, no spaces.
const HEADER_VALUE = /^[\x21-\x7e]+$/
const LOOPBACK = new Set(['127.0.0.1', 'localhost', '[::1]'])

// A delivery's credentials, and whether it keeps the model held before: a
// redelivery that brings a new Identity Token while the platform no longer
// supplies the model key the Work started with.
export function parseDelivery(body: unknown): { credentials: WorkCredentials; keepModel: boolean } {
  const credentials = parseCredentials(body)
  const keepModel = isObject(body) && body.keep_model === true
  if (keepModel && credentials.model !== undefined) throw new InvalidCredentialsError('keep_model excludes model')
  return { credentials, keepModel }
}

function parseCredentials(body: unknown): WorkCredentials {
  if (!isObject(body)) throw new InvalidCredentialsError('body must be an object')
  const { model, gateway, api } = body
  const result: WorkCredentials = { gateway: parseGateway(gateway) }
  if (model !== undefined && model !== null) result.model = parseModel(model)
  if (api !== undefined && api !== null) result.api = parseApi(api, result.gateway.url)
  return result
}

function parseModel(model: unknown): NonNullable<WorkCredentials['model']> {
  if (!isObject(model)) throw new InvalidCredentialsError('model must be an object')
  const url = parseUrl(model.base_url, 'model.base_url')
  // Plain http reaches only a vendor on this machine.
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && LOOPBACK.has(url.hostname))) {
    throw new InvalidCredentialsError('model.base_url must be an https URL')
  }
  return { baseUrl: url.href, key: headerValue(model.key, 'model.key') }
}

// The native API lies on the gateway's origin, the only one the Work's
// identity is sent to.
function parseApi(api: unknown, gatewayUrl: string): NonNullable<WorkCredentials['api']> {
  if (!isObject(api)) throw new InvalidCredentialsError('api must be an object')
  const url = parseUrl(api.url, 'api.url')
  if (url.origin !== new URL(gatewayUrl).origin) throw new InvalidCredentialsError('api.url must lie on gateway.url\'s origin')
  return { url: url.href }
}

function parseGateway(gateway: unknown): WorkCredentials['gateway'] {
  if (!isObject(gateway)) throw new InvalidCredentialsError('gateway must be an object')
  const url = parseUrl(gateway.url, 'gateway.url')
  if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new InvalidCredentialsError('gateway.url must be an http(s) URL')
  const result: WorkCredentials['gateway'] = {
    url: url.href,
    workId: headerValue(gateway.work_id, 'gateway.work_id'),
    identityToken: headerValue(gateway.identity_token, 'gateway.identity_token'),
    workCredential: headerValue(gateway.work_credential, 'gateway.work_credential'),
  }
  if (gateway.space_token !== undefined && gateway.space_token !== null && gateway.space_token !== '') {
    result.spaceToken = headerValue(gateway.space_token, 'gateway.space_token')
  }
  return result
}

function parseUrl(value: unknown, field: string): URL {
  if (typeof value !== 'string') throw new InvalidCredentialsError(`${field} must be a URL`)
  let url: URL
  try {
    url = new URL(value)
  } catch {
    throw new InvalidCredentialsError(`${field} must be a URL`)
  }
  // Paths are appended to it; credentials, a query or a fragment in it would
  // not survive that.
  if (url.username !== '' || url.password !== '' || url.search !== '' || url.hash !== '') {
    throw new InvalidCredentialsError(`${field} must carry no user, query or fragment`)
  }
  return url
}

function headerValue(value: unknown, field: string): string {
  if (typeof value !== 'string' || !HEADER_VALUE.test(value)) throw new InvalidCredentialsError(`${field} must be a non-empty token`)
  return value
}
