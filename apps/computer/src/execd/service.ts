import { mkdir } from 'node:fs/promises'
import { InvalidCredentialsError, type CredentialStore } from '../proxy/credentials.ts'
import { deleteVariable, mergeEnvironment, prependPath } from './environment.ts'
import { InvalidManifestError, parseManifest, type WorkInputs } from './inputs.ts'
import { openPipe, type PipeInput, type PipeSink } from './pipe.ts'
import type { ProcessTable } from './processes.ts'
import { Reconciliation } from './reconciliation.ts'
import { listDirectory, openApiFile, statEntry, streamFile } from './work-files.ts'
import { applyTelemetry } from './telemetry.ts'
import { mapCwd } from './work-paths.ts'
import { InvalidEnvironmentError, validId, type WorkEnvironment, type Works } from './works.ts'

// The execd API of each Work on this machine, as the Computer Gateway relays
// it. Statuses and bodies follow OpenSandbox execd's.

export interface ExecRequest {
  workId: string
  method: string
  path: string
  token: string
  body?: unknown
}

export interface ExecResponse {
  status: number
  body?: unknown
}

// A pipe of a process, or a read of one of the Work's files.
export type StreamOpen = PipeOpen | FileOpen

export interface PipeOpen {
  workId: string
  processId: string
  since: number
  token: string
}

export interface FileOpen {
  kind: 'file'
  workId: string
  // Relative to the Work root.
  path: string
  token: string
}

export interface ServiceOptions {
  works: Works
  processes: ProcessTable
  // Where Work processes find the bundled Claude Code, first on PATH.
  claudeBinDir?: string
  credentials: CredentialStore
  inputs: WorkInputs
  // The local proxy's origin, http://127.0.0.1:<port>.
  proxyOrigin: () => string
  // The machine's own environment and home, which every process starts from.
  baseEnv: NodeJS.ProcessEnv
  home: string
  log: (message: string) => void
  // The machine's platform, which decides how environment names compare.
  platform?: NodeJS.Platform
}

const unauthorized: ExecResponse = { status: 401, body: { error: 'Unauthorized: invalid or missing header X-EXECD-ACCESS-TOKEN' } }

function notFound(id: string): ExecResponse {
  return { status: 404, body: { code: 'CONTEXT_NOT_FOUND', message: `pty session ${id} not found` } }
}

// How long a run waits for the Work's inputs being brought in before it
// starts without them.
const INPUTS_WAIT_MS = 2 * 60_000

// The relay path of the machine's own reconciliation, which names no Work.
export const RECONCILIATION_PATH = '/reconciliation'

export class ExecdService {
  private readonly options: ServiceOptions
  // Each connection's reconciliation, which every start waits for.
  readonly reconciliation: Reconciliation

  constructor(options: ServiceOptions) {
    this.options = options
    this.reconciliation = new Reconciliation(options)
  }

  async request(request: ExecRequest): Promise<ExecResponse> {
    const { works } = this.options
    // The Gateway checked the control token and relays it without a Work.
    if (request.workId === '' && request.path === RECONCILIATION_PATH) {
      if (request.method === 'GET') return this.reconciliation.inventory()
      return request.method === 'POST' ? this.reconciliation.apply(request.body) : { status: 405 }
    }
    if (request.path === '/') {
      if (request.method !== 'PUT' || !validId(request.workId)) return { status: 404 }
      try {
        return { status: 200, body: { work_root: await works.put(request.workId, request.body) } }
      } catch (error) {
        if (error instanceof InvalidEnvironmentError) return { status: 400, body: { code: 'INVALID_REQUEST_BODY', message: error.message } }
        throw error
      }
    }
    if (request.path === '/credentials') return this.credentials(request)
    if (request.path === '/inputs') return this.inputs(request)
    const access = await works.authorize(request.workId, request.token)
    if (access.kind === 'unknown') return { status: 404 }
    if (access.kind === 'denied') return unauthorized
    return this.route(access.work, request)
  }

  // Like the environment PUT, the Gateway checked the control token and
  // relays it without a Work token.
  private async credentials(request: ExecRequest): Promise<ExecResponse> {
    const { method, workId } = request
    if (method !== 'PUT' && method !== 'DELETE') return { status: 405 }
    if (!(await this.options.works.known(workId))) return { status: 404 }
    const { credentials } = this.options
    if (method === 'DELETE') {
      await credentials.delete(workId)
      this.options.inputs.forget(workId)
      return { status: 204 }
    }
    try {
      await credentials.put(workId, request.body)
    } catch (error) {
      if (error instanceof InvalidCredentialsError) return { status: 400, body: { code: 'INVALID_REQUEST_BODY', message: error.message } }
      throw error
    }
    this.options.inputs.credentialsDelivered(workId)
    return { status: 204 }
  }

  // The Work's input File Space, relayed like the credentials. It is
  // accepted at once and brought into the Work root in the background.
  private async inputs(request: ExecRequest): Promise<ExecResponse> {
    const { method, workId } = request
    if (method !== 'PUT') return { status: 405 }
    const { works, inputs } = this.options
    if (!(await works.known(workId))) return { status: 404 }
    try {
      inputs.submit(workId, works.root(workId), parseManifest(request.body))
    } catch (error) {
      if (error instanceof InvalidManifestError) return { status: 400, body: { code: 'INVALID_REQUEST_BODY', message: error.message } }
      throw error
    }
    return { status: 202 }
  }

  private async route(work: WorkEnvironment, request: ExecRequest): Promise<ExecResponse> {
    const { method, path, workId } = request
    // The ping also tells the platform whether this connection's
    // reconciliation round is decided, so a decided one costs it no call.
    if (path === '/ping') return method === 'GET' ? { status: 200, body: { reconciled: !this.reconciliation.pending() } } : { status: 405 }
    if (path === '/pty') return method === 'POST' ? this.create(work, workId, request.body) : { status: 405 }
    if (path === '/files' || path.startsWith('/files?')) return method === 'GET' ? this.list(work, path) : { status: 405 }
    if (path === '/files/stat' || path.startsWith('/files/stat?')) return method === 'GET' ? this.stat(work, path) : { status: 405 }
    const match = /^\/pty\/([^/]+)$/.exec(path)
    if (match === null) return { status: 404 }
    const id = decodeURIComponent(match[1])
    if (!validId(id)) return notFound(id)
    const { processes } = this.options
    if (method === 'GET') {
      const phase = await processes.phase(workId, id)
      if (phase === undefined) return notFound(id)
      return {
        status: 200,
        body: { session_id: id, running: phase.kind === 'running', output_offset: await processes.outputOffset(workId, id) },
      }
    }
    if (method === 'DELETE') return (await processes.delete(workId, id)) ? { status: 200 } : notFound(id)
    return { status: 405 }
  }

  private async list(work: WorkEnvironment, path: string): Promise<ExecResponse> {
    const query = new URLSearchParams(path.slice('/files'.length + 1))
    const listing = await listDirectory(work.root, query.get('path') ?? '')
    if (listing.kind === 'missing') return { status: 404, body: { code: 'FILE_NOT_FOUND', message: 'no such directory in the Work root' } }
    if (listing.kind === 'not_directory') return { status: 400, body: { code: 'NOT_A_DIRECTORY', message: 'the path names a file' } }
    return { status: 200, body: { entries: listing.entries } }
  }

  private async stat(work: WorkEnvironment, path: string): Promise<ExecResponse> {
    const query = new URLSearchParams(path.slice('/files/stat'.length + 1))
    const entry = await statEntry(work.root, query.get('path') ?? '')
    if (entry === undefined) return { status: 404, body: { code: 'FILE_NOT_FOUND', message: 'no such file or directory in the Work root' } }
    return { status: 200, body: entry }
  }

  private async create(work: WorkEnvironment, workId: string, body: unknown): Promise<ExecResponse> {
    if (typeof body !== 'object' || body === null || Array.isArray(body)) {
      return { status: 400, body: { code: 'INVALID_REQUEST_BODY', message: 'error parsing request' } }
    }
    const { cwd = '', command = '' } = body as Record<string, unknown>
    if (typeof cwd !== 'string' || typeof command !== 'string') {
      return { status: 400, body: { code: 'INVALID_REQUEST_BODY', message: 'error parsing request' } }
    }
    // Nothing new runs before the platform decided what ran while it was away.
    if (this.reconciliation.pending()) {
      return { status: 409, body: { code: 'RECONCILIATION_PENDING', message: 'the machine reconnected and waits for its reconciliation' } }
    }
    const mappedCwd = mapCwd(cwd, work.root)
    try {
      await mkdir(mappedCwd, { recursive: true })
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      return { status: 500, body: { code: 'RUNTIME_ERROR', message: `error creating pty session: ${message}` } }
    }
    const id = await this.options.processes.reserve(workId, { cwd: mappedCwd, command })
    return { status: 201, body: { session_id: id } }
  }

  async openStream(open: StreamOpen, sink: PipeSink): Promise<{ status: number } | PipeInput> {
    const access = await this.options.works.authorize(open.workId, open.token)
    if (access.kind === 'unknown') return { status: 404 }
    if (access.kind === 'denied') return { status: 401 }
    if ('kind' in open) {
      const file = await openApiFile(access.work.root, open.path)
      if ('status' in file) return file
      // The bytes follow once the stream is open.
      setImmediate(() => void streamFile(file.handle, sink, this.options.log))
      return { text: () => {}, binary: () => {} }
    }
    if (!validId(open.processId) || !Number.isSafeInteger(open.since) || open.since < 0) return { status: 404 }
    return openPipe(
      {
        table: this.options.processes,
        workId: open.workId,
        id: open.processId,
        since: open.since,
        env: (proxyToken) => this.runEnv(open.workId, access.work, proxyToken),
        // A run starts once the reconciliation decided what may run, and
        // its first turn sees the inputs the latest manifest lists.
        ready: async (signal) => {
          await this.reconciliation.settled(signal)
          if (!signal.aborted) await this.options.inputs.settled(open.workId, INPUTS_WAIT_MS)
        },
        log: this.options.log,
      },
      sink,
    )
  }

  // The machine's environment, then the Work's, then what Talgent Computer
  // owns: the machine's HOME, the Work root, the bundled Claude Code first on
  // PATH, the run's way to the local proxy and, when the platform named it,
  // where its telemetry goes, in place of the machine's. A Work on a Model
  // Configuration reaches its model only through the proxy, with the run's
  // token, whether or not its credentials were delivered yet: it never falls
  // back to the machine's own login.
  private async runEnv(workId: string, work: WorkEnvironment, proxyToken: string): Promise<NodeJS.ProcessEnv> {
    const { baseEnv, home, claudeBinDir: bin, proxyOrigin, platform = process.platform } = this.options
    const proxyUrl = `${proxyOrigin()}/w/${workId}`
    const model = work.modelProxy ? { ANTHROPIC_BASE_URL: `${proxyUrl}/model`, ANTHROPIC_AUTH_TOKEN: proxyToken } : {}
    const env = mergeEnvironment(platform, baseEnv, work.environment, {
      HOME: home,
      TALGENT_WORK_ROOT: work.root,
      TALGENT_PROXY_URL: proxyUrl,
      TALGENT_PROXY_TOKEN: proxyToken,
      ...model,
    })
    if (bin !== undefined) prependPath(env, bin, platform)
    if (work.modelProxy) deleteVariable(env, 'ANTHROPIC_API_KEY', platform)
    if (work.telemetry !== undefined) applyTelemetry(env, workId, work.telemetry, platform)
    return env
  }
}
