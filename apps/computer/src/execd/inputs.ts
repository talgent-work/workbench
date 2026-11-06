import { randomBytes } from 'node:crypto'
import { chmod, lstat, mkdir, open, readdir, readFile, rename, rm, utimes } from 'node:fs/promises'
import { join } from 'node:path'
import { isObject } from '../json.ts'
import { Limit } from '../limit.ts'
import type { WorkCredentials } from '../proxy/credentials.ts'
import { isErrno } from '../store.ts'
import { writeJsonAtomic } from './shim.ts'

// A Work's input File Space, kept as read-only copies in <work root>/inputs/.
// Each manifest is the
// whole File Space; Talgent Computer brings the directory to it in the
// background, one manifest at a time per Work: a newer one listing other
// versions supersedes the one in progress, one listing the same versions
// follows it, and failed downloads are retried while their links last, then
// with the next manifest. What it last downloaded is recorded outside the
// Work root, so a restart only fetches what changed; a copy changed on the
// machine, by size or modification time, is fetched again.

export interface InputFile {
  name: string
  size: number
  updatedAt: string
  url: string
}

interface Recorded {
  size: number
  updatedAt: string
}

export interface DownloadTimeouts {
  // Until the response headers arrive.
  connectMs: number
  // Between two chunks of the body.
  idleMs: number
}

const DEFAULT_TIMEOUTS: DownloadTimeouts = { connectMs: 30_000, idleMs: 60_000 }
// The waits before each retry of a manifest whose downloads failed. They
// all fall within the 15-minute lifetime of the manifest's Access Links;
// past them the platform's next manifest, sent at least every 10 minutes,
// brings fresh links.
export const RETRY_DELAYS_MS = [10_000, 30_000, 90_000, 300_000]
const MAX_FILES = 10_000
const DOWNLOADS_PER_WORK = 2
const TEMP_SUFFIX = '.talgent-download'

export class InvalidManifestError extends Error {}

// The manifest's files; its message names the invalid field, never a value.
export function parseManifest(body: unknown): InputFile[] {
  if (!isObject(body) || !Array.isArray(body.files)) throw new InvalidManifestError('files must be an array')
  if (body.files.length > MAX_FILES) throw new InvalidManifestError(`at most ${MAX_FILES} files`)
  const names = new Set<string>()
  return body.files.map((file: unknown, index: number) => {
    if (!isObject(file)) throw new InvalidManifestError(`files[${index}] must be an object`)
    const { name, size, updated_at: updatedAt, url } = file
    if (typeof name !== 'string' || !safeName(name)) throw new InvalidManifestError(`files[${index}].name must be one file name`)
    if (names.has(name)) throw new InvalidManifestError(`files[${index}].name is listed twice`)
    names.add(name)
    if (typeof size !== 'number' || !Number.isSafeInteger(size) || size < 0) throw new InvalidManifestError(`files[${index}].size must be a byte count`)
    if (typeof updatedAt !== 'string' || !Number.isFinite(Date.parse(updatedAt))) throw new InvalidManifestError(`files[${index}].updated_at must be a time`)
    if (typeof url !== 'string' || !httpUrl(url)) throw new InvalidManifestError(`files[${index}].url must be an http(s) URL`)
    return { name, size, updatedAt, url }
  })
}

// One path segment that names a file in the inputs directory and nothing
// else; our own temporary files are not nameable either.
function safeName(name: string): boolean {
  return name !== '' && name !== '.' && name !== '..' && Buffer.byteLength(name) <= 255 &&
    !/[/\\\0]/.test(name) && !name.endsWith(TEMP_SUFFIX)
}

// Whether this machine's file system can hold a file of that name. Windows
// reserves some characters and device names, and drops a trailing dot or
// space; a name with a colon would write an alternate data stream.
export function holdable(name: string, platform: NodeJS.Platform): boolean {
  if (platform !== 'win32') return true
  // eslint-disable-next-line no-control-regex
  if (/[<>:"|?*\x00-\x1f]/.test(name) || /[. ]$/.test(name)) return false
  return !/^(con|prn|aux|nul|com[0-9¹²³]|lpt[0-9¹²³])(\.|$)/i.test(name)
}

function httpUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return url.protocol === 'https:' || url.protocol === 'http:'
  } catch {
    return false
  }
}

interface Job {
  root: string
  files: InputFile[]
  // Retries of this manifest so far.
  attempt: number
}

interface Slot {
  next?: Job
  // The manifest being brought in, and how to stop it.
  job?: Job
  current?: AbortController
  running?: Promise<void>
}

export interface InputsOptions {
  // Records of what was downloaded, outside every Work root.
  recordsDir: string
  credentials: { get(workId: string): Promise<WorkCredentials | undefined> }
  log: (message: string) => void
  timeouts?: DownloadTimeouts
  retryDelaysMs?: number[]
  // The machine's platform, which decides the file names it can hold.
  platform?: NodeJS.Platform
}

export class WorkInputs {
  private readonly options: InputsOptions
  private readonly timeouts: DownloadTimeouts
  private readonly retryDelaysMs: number[]
  private readonly platform: NodeJS.Platform
  private readonly slots = new Map<string, Slot>()
  // The latest manifest of Works whose downloads wait for credentials.
  private readonly waiting = new Map<string, Job>()
  // The retry due of Works whose latest manifest failed.
  private readonly retries = new Map<string, NodeJS.Timeout>()

  constructor(options: InputsOptions) {
    this.options = options
    this.timeouts = options.timeouts ?? DEFAULT_TIMEOUTS
    this.retryDelaysMs = options.retryDelaysMs ?? RETRY_DELAYS_MS
    this.platform = options.platform ?? process.platform
  }

  // Brings the Work's inputs to files in the background.
  submit(workId: string, root: string, files: InputFile[]): void {
    const kept = files.filter((file) => holdable(file.name, this.platform))
    if (kept.length < files.length) this.options.log(`Work ${workId} inputs: ${files.length - kept.length} file(s) have names this machine cannot hold; skipped`)
    this.enqueue(workId, { root, files: kept, attempt: 0 })
  }

  // The Work's credentials arrived: a manifest that waited for them runs again.
  credentialsDelivered(workId: string): void {
    const job = this.waiting.get(workId)
    if (job !== undefined) this.enqueue(workId, { ...job, attempt: 0 })
  }

  // The Work's credentials were withdrawn: its downloads stop, and the next
  // manifest, which follows a new delivery, starts them again.
  forget(workId: string): void {
    this.cancelRetry(workId)
    this.waiting.delete(workId)
    const slot = this.slots.get(workId)
    if (slot === undefined) return
    slot.next = undefined
    slot.current?.abort()
  }

  // The Work is gone from this machine: its downloads stop, wait for
  // nothing, and its record goes.
  async remove(workId: string): Promise<void> {
    this.forget(workId)
    await this.slots.get(workId)?.running?.catch(() => {})
    await rm(this.recordFile(workId), { force: true })
  }

  // Resolves once the Work's inputs match the latest manifest as far as they
  // can, or after timeoutMs.
  async settled(workId: string, timeoutMs = Infinity): Promise<void> {
    const running = this.slots.get(workId)?.running
    if (running === undefined || timeoutMs === Infinity) return running
    let timer: NodeJS.Timeout | undefined
    try {
      await Promise.race([running, new Promise<void>((resolve) => { timer = setTimeout(resolve, timeoutMs) })])
    } finally {
      clearTimeout(timer)
    }
  }

  private enqueue(workId: string, job: Job): void {
    this.cancelRetry(workId)
    this.waiting.delete(workId)
    let slot = this.slots.get(workId)
    if (slot === undefined) {
      slot = {}
      this.slots.set(workId, slot)
    }
    slot.next = job
    // A manifest listing the same versions only brings fresh links: the one
    // in progress goes on, so a download longer than the platform's refresh
    // interval still ends, and the newer one follows with what it left.
    if (slot.job === undefined || !sameVersions(slot.job.files, job.files)) slot.current?.abort()
    if (slot.running === undefined) slot.running = this.drain(workId, slot)
  }

  private async drain(workId: string, slot: Slot): Promise<void> {
    while (slot.next !== undefined) {
      const job = slot.next
      slot.next = undefined
      const current = new AbortController()
      slot.current = current
      slot.job = job
      let failed = true
      try {
        failed = await this.reconcile(workId, job, current.signal)
      } catch (error) {
        this.options.log(`Work ${workId} inputs: ${reasonOf(error)}`)
      }
      slot.job = undefined
      if (failed && !current.signal.aborted && slot.next === undefined) this.retry(workId, job)
    }
    this.slots.delete(workId)
  }

  // Runs the manifest again after the next delay, unless a newer one comes.
  private retry(workId: string, job: Job): void {
    const delay = this.retryDelaysMs[job.attempt]
    if (delay === undefined) return this.options.log(`Work ${workId} inputs: retries ran out; the next manifest tries again`)
    const timer = setTimeout(() => {
      this.retries.delete(workId)
      this.enqueue(workId, { ...job, attempt: job.attempt + 1 })
    }, delay)
    timer.unref()
    this.retries.set(workId, timer)
  }

  private cancelRetry(workId: string): void {
    clearTimeout(this.retries.get(workId))
    this.retries.delete(workId)
  }

  // Brings the directory to job; true when a download failed.
  private async reconcile(workId: string, job: Job, signal: AbortSignal): Promise<boolean> {
    const dir = join(job.root, 'inputs')
    await openForChange(dir)
    try {
      const recorded = await this.readRecord(workId)
      const listed = new Set(job.files.map((file) => file.name))
      for (const name of await readdir(dir)) {
        if (!listed.has(name)) await rm(join(dir, name), { recursive: true, force: true })
      }
      const next: Record<string, Recorded> = {}
      const stale: InputFile[] = []
      for (const file of job.files) {
        const previous = recorded[file.name]
        if (previous !== undefined) next[file.name] = previous
        const current = previous !== undefined && previous.size === file.size && previous.updatedAt === file.updatedAt
        if (!current || !(await present(join(dir, file.name), file))) stale.push(file)
      }
      const failed = stale.length > 0 && (await this.download(workId, job, dir, stale, next, signal))
      await mkdir(this.options.recordsDir, { recursive: true, mode: 0o700 })
      writeJsonAtomic(this.recordFile(workId), next)
      return failed
    } finally {
      await chmod(dir, 0o555).catch(() => {})
    }
  }

  // Downloads the stale files; true when one failed.
  private async download(workId: string, job: Job, dir: string, stale: InputFile[], next: Record<string, Recorded>, signal: AbortSignal): Promise<boolean> {
    const { log } = this.options
    // Waiting first: a delivery while the store is read wakes this job.
    this.waiting.set(workId, job)
    const credentials = await this.options.credentials.get(workId)
    if (credentials === undefined) {
      if (!signal.aborted) log(`Work ${workId} inputs: ${stale.length} file(s) wait for the Work's credentials`)
      return false
    }
    if (this.waiting.get(workId) === job) this.waiting.delete(workId)
    const { gateway } = credentials
    const origin = new URL(gateway.url).origin
    const headers: Record<string, string> = { 'X-Talgent-Work-Id': gateway.workId, 'X-Talgent-Work-Credential': gateway.workCredential }
    if (gateway.spaceToken !== undefined) headers['X-Talgent-Space-Token'] = gateway.spaceToken
    const limit = new Limit(DOWNLOADS_PER_WORK)
    let failed = false
    await Promise.all(stale.map((file) => limit.run(async () => {
      if (signal.aborted) return
      // The Work's identity goes only to the platform that issued it.
      if (new URL(file.url).origin !== origin) return log(`Work ${workId} inputs: ${JSON.stringify(file.name)} is not on the platform's origin; skipped`)
      try {
        await this.fetchFile(file, dir, headers, signal)
        next[file.name] = { size: file.size, updatedAt: file.updatedAt }
      } catch (error) {
        if (signal.aborted) return
        failed = true
        log(`Work ${workId} inputs: ${JSON.stringify(file.name)} not downloaded, the previous copy stays: ${reasonOf(error)}`)
      }
    })))
    return failed
  }

  // Downloads file into a temporary file beside its place, then renames it
  // over the previous copy, read-only.
  private async fetchFile(file: InputFile, dir: string, headers: Record<string, string>, signal: AbortSignal): Promise<void> {
    const tmp = join(dir, `.${randomBytes(6).toString('hex')}${TEMP_SUFFIX}`)
    const controller = new AbortController()
    const stop = () => controller.abort(new DownloadError('superseded'))
    signal.addEventListener('abort', stop)
    let timer = setTimeout(() => controller.abort(new DownloadError('no response in time')), this.timeouts.connectMs)
    try {
      // A redirect could carry the identity headers elsewhere.
      const response = await fetch(file.url, { headers, redirect: 'error', signal: controller.signal })
      if (response.status !== 200 || response.body === null) {
        await response.body?.cancel().catch(() => {})
        throw new DownloadError(`answered ${response.status}`)
      }
      const output = await open(tmp, 'wx', 0o600)
      try {
        let received = 0
        const reader = response.body.getReader()
        for (;;) {
          clearTimeout(timer)
          timer = setTimeout(() => controller.abort(new DownloadError('stalled')), this.timeouts.idleMs)
          const { done, value } = await reader.read()
          if (done) break
          received += value.length
          if (received > file.size) throw new DownloadError('larger than listed')
          await output.write(value)
        }
        if (received !== file.size) throw new DownloadError('smaller than listed')
      } finally {
        await output.close()
      }
      // The modification time marks the copy as the listed version.
      const updatedAt = new Date(file.updatedAt)
      await utimes(tmp, updatedAt, updatedAt)
      await chmod(tmp, 0o444)
      const target = join(dir, file.name)
      // Windows refuses to rename over a read-only file.
      if (this.platform === 'win32') await chmod(target, 0o644).catch(() => {})
      await rename(tmp, target)
    } finally {
      clearTimeout(timer)
      signal.removeEventListener('abort', stop)
      controller.abort()
      await rm(tmp, { force: true })
    }
  }

  private async readRecord(workId: string): Promise<Record<string, Recorded>> {
    try {
      const parsed = JSON.parse(await readFile(this.recordFile(workId), 'utf8')) as unknown
      return isObject(parsed) ? (parsed as Record<string, Recorded>) : {}
    } catch (error) {
      if (isErrno(error, 'ENOENT') || error instanceof SyntaxError) return {}
      throw error
    }
  }

  private recordFile(workId: string): string {
    return join(this.options.recordsDir, `${workId}.json`)
  }
}

// Makes dir a real directory this user may change, replacing anything else
// a Work process left in its place.
async function openForChange(dir: string): Promise<void> {
  try {
    const info = await lstat(dir)
    if (!info.isDirectory()) await rm(dir, { recursive: true, force: true })
  } catch (error) {
    if (!isErrno(error, 'ENOENT')) throw error
  }
  await mkdir(dir, { recursive: true, mode: 0o755 })
  await chmod(dir, 0o755)
}

// Whether two manifests list the same files at the same versions, whatever
// their links.
function sameVersions(a: InputFile[], b: InputFile[]): boolean {
  if (a.length !== b.length) return false
  const versions = new Map(a.map((file) => [file.name, `${file.size}/${file.updatedAt}`]))
  return b.every((file) => versions.get(file.name) === `${file.size}/${file.updatedAt}`)
}

// Whether path holds the listed version of file, by size and modification time.
async function present(path: string, file: InputFile): Promise<boolean> {
  try {
    const info = await lstat(path)
    return info.isFile() && info.size === file.size && Math.abs(info.mtimeMs - Date.parse(file.updatedAt)) < 1
  } catch (error) {
    if (isErrno(error, 'ENOENT')) return false
    throw error
  }
}

class DownloadError extends Error {}

// Why a download failed, without its URL or headers.
function reasonOf(error: unknown): string {
  if (error instanceof DownloadError) return error.message
  const cause = (error as { cause?: { code?: unknown } } | undefined)?.cause
  if (typeof cause?.code === 'string') return cause.code
  const code = (error as NodeJS.ErrnoException | undefined)?.code
  if (typeof code === 'string') return code
  return error instanceof Error && error.name === 'AbortError' ? 'aborted' : 'failed'
}
