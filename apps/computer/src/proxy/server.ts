import { mkdir, readFile } from 'node:fs/promises'
import { createServer, type IncomingHttpHeaders, type IncomingMessage, type OutgoingHttpHeaders, type Server, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'
import { dirname } from 'node:path'
import { writeJsonAtomic } from '../execd/shim.ts'
import { validId } from '../execd/works.ts'
import { isErrno } from '../store.ts'
import type { WorkCredentials } from './credentials.ts'
import { answerJson, DEFAULT_TIMEOUTS, forward, requestHeaders, type Timeouts } from './forward.ts'
import { ContentHandles } from './content-handles.ts'
import { contentBase, rewritingContentLinks } from './content-links.ts'
import { completeRequest } from './publication.ts'
import type { ReadBody } from './request-body.ts'
import { ARTIFACT_COMMANDS, completeArtifactCommand } from './api-publication.ts'

// The local proxy: Work processes reach the model vendor, the Talgent MCP
// endpoint and the platform's native API through it with their run's token,
// and it adds the Work's credentials, which never reach the process.

export interface ProxyAccess {
  // Whether token is the current run token of a process of workId.
  tokenValid(workId: string, token: string): Promise<boolean>
  credentials(workId: string): Promise<WorkCredentials | undefined>
  // Whether the Work's model is a Model Configuration reached through here.
  modelProxy(workId: string): Promise<boolean>
  // The Work's root directory, which publication reads files from.
  workRoot(workId: string): string
}

// The upstream paths, under the model's base path, that carry the key.
const KEYED_MODEL_PATHS = new Set(['/v1/messages', '/v1/messages/count_tokens'])
const ROUTE = /^\/w\/([^/]+)\/(model|mcp|content|api)(\/.*)?$/
// An API call's path: the run's token, then the procedure or Access Link.
const API_PATH = /^\/([^/]+)(\/.*)$/
const CONTENT_HANDLE = /^\/([A-Za-z0-9_-]+)$/

export class LocalProxy {
  private readonly portFile: string
  private readonly access: ProxyAccess
  private readonly log: (message: string) => void
  private readonly server: Server
  private readonly timeouts: Timeouts
  private readonly contentHandles: ContentHandles
  private port = 0

  // portFile keeps the port across restarts, so running processes keep
  // reaching the proxy.
  constructor(options: { portFile: string; access: ProxyAccess; log: (message: string) => void; timeouts?: Timeouts; contentHandles?: ContentHandles }) {
    this.portFile = options.portFile
    this.access = options.access
    this.log = options.log
    this.timeouts = options.timeouts ?? DEFAULT_TIMEOUTS
    this.contentHandles = options.contentHandles ?? new ContentHandles()
    this.server = createServer((request, response) => void this.serve(request, response))
    this.server.timeout = this.timeouts.idleMs
  }

  origin(): string {
    if (this.port === 0) throw new Error('the local proxy has not started')
    return `http://127.0.0.1:${this.port}`
  }

  async start(): Promise<void> {
    const kept = await this.keptPort()
    let port: number
    if (kept === undefined) {
      port = await this.listen(0)
    } else {
      try {
        port = await this.listen(kept)
      } catch (error) {
        if (!isErrno(error, 'EADDRINUSE')) throw error
        port = await this.listen(0)
        this.log(`local proxy port ${kept} is taken; it moved to ${port}, and processes started before lose the proxy until they run again`)
      }
    }
    this.port = port
    if (port !== kept) {
      await mkdir(dirname(this.portFile), { recursive: true, mode: 0o700 })
      writeJsonAtomic(this.portFile, { port })
    }
  }

  async close(): Promise<void> {
    if (!this.server.listening) return
    const closed = new Promise<void>((resolve) => this.server.close(() => resolve()))
    this.server.closeAllConnections()
    await closed
  }

  private listen(port: number): Promise<number> {
    return new Promise((resolve, reject) => {
      const onError = (error: Error) => reject(error)
      this.server.once('error', onError)
      this.server.listen(port, '127.0.0.1', () => {
        this.server.off('error', onError)
        resolve((this.server.address() as AddressInfo).port)
      })
    })
  }

  private async keptPort(): Promise<number | undefined> {
    try {
      const { port } = JSON.parse(await readFile(this.portFile, 'utf8')) as { port?: unknown }
      return Number.isInteger(port) && (port as number) > 0 && (port as number) < 65536 ? (port as number) : undefined
    } catch (error) {
      if (isErrno(error, 'ENOENT') || error instanceof SyntaxError) return undefined
      throw error
    }
  }

  private async serve(request: IncomingMessage, response: ServerResponse): Promise<void> {
    try {
      await this.route(request, response)
    } catch (error) {
      this.log(`local proxy: ${error instanceof Error ? error.message : String(error)}`)
      answerJson(response, 500, { code: 'INTERNAL', message: 'the local proxy failed' })
    }
  }

  private async route(request: IncomingMessage, response: ServerResponse): Promise<void> {
    const raw = request.url ?? '/'
    const queryAt = raw.indexOf('?')
    const pathname = queryAt < 0 ? raw : raw.slice(0, queryAt)
    const search = queryAt < 0 ? '' : raw.slice(queryAt)
    const match = ROUTE.exec(pathname)
    if (match === null || !validId(match[1])) return notFound(response)
    const [, workId, route, rest = ''] = match
    if (route === 'content') return this.content(request, response, workId, rest)
    if (route === 'api') return this.api(request, response, workId, rest, search)
    if (!(await this.access.tokenValid(workId, presentedToken(request.headers)))) {
      return answerJson(response, 401, { code: 'UNAUTHORIZED', message: 'present the token of a run of this Work' })
    }
    if (route === 'model' && !(await this.access.modelProxy(workId))) return notFound(response)
    const credentials = await this.access.credentials(workId)
    // A Work on a Model Configuration waits for its key rather than reach
    // any other model.
    if (credentials === undefined || (route === 'model' && credentials.model === undefined)) {
      return answerJson(response, 503, { code: 'CREDENTIALS_NOT_DELIVERED', message: 'the Work\'s credentials were not delivered' })
    }
    const headers = requestHeaders(request.headers)
    let target: URL | undefined
    let body: ReadBody | undefined
    if (route === 'model') {
      const model = credentials.model as NonNullable<WorkCredentials['model']>
      target = below(model.baseUrl, rest, search)
      if (target === undefined) return notFound(response)
      if (request.method === 'POST' && KEYED_MODEL_PATHS.has(rest)) headers.authorization = `Bearer ${model.key}`
    } else {
      const { gateway } = credentials
      target = below(gateway.url, rest, search)
      if (target === undefined) return notFound(response)
      addIdentity(headers, gateway)
      addIdentityToken(headers, gateway)
      if (request.method === 'POST') {
        const completed = await completeRequest(request, this.access.workRoot(workId))
        if (completed.kind === 'answer') return answerRpc(response, completed.body)
        if (completed.kind === 'pass') body = completed.body
        else {
          headers['content-length'] = String(completed.body.length)
          body = { chunks: [completed.body] }
        }
      }
    }
    const localLink = (query: string) => `${this.origin()}/w/${workId}/content/${this.contentHandles.issue(workId, query)}`
    forward(request, response, target, headers, {
      body,
      // The model gets links to the platform's content it can fetch itself.
      relay: route === 'mcp' ? rewritingContentLinks(credentials.gateway.url, localLink) : undefined,
      timeouts: this.timeouts,
      fail: (reason) => this.log(`local proxy: Work ${workId} ${route} upstream failed: ${reason}`),
    })
  }

  // A native API call of a Work's plain HTTP client, such as curl, which
  // presents the run's token as the first segment of the path. The proxy
  // adds the Work's identity, and for an Artifact command naming a file of
  // the Work, the file's bytes.
  private async api(request: IncomingMessage, response: ServerResponse, workId: string, rest: string, search: string): Promise<void> {
    const match = API_PATH.exec(rest)
    if (match === null) return notFound(response)
    const [, token, path] = match
    // Refused with the native API's own error codes, which the Work's skill
    // reads, unlike the other routes' clients.
    if (!(await this.access.tokenValid(workId, token))) {
      return answerJson(response, 401, { code: 'unauthenticated', message: 'present the token of a run of this Work' })
    }
    const credentials = await this.access.credentials(workId)
    if (credentials === undefined) {
      return answerJson(response, 503, { code: 'unavailable', message: 'the Work\'s credentials were not delivered' })
    }
    if (credentials.api === undefined) return notFound(response)
    const target = below(credentials.api.url, path, search)
    if (target === undefined) return notFound(response)
    // The entry takes the Work's Identity Token alone, and the Space Token
    // its Artifact commands write with.
    const headers = requestHeaders(request.headers)
    addIdentityToken(headers, credentials.gateway)
    addSpaceToken(headers, credentials.gateway)
    let body: ReadBody | undefined
    if (request.method === 'POST' && ARTIFACT_COMMANDS.has(path)) {
      const completed = await completeArtifactCommand(request, this.access.workRoot(workId))
      if (completed.kind === 'refuse') {
        // As the native API refuses a source it cannot publish.
        if (completed.missing) return answerJson(response, 404, { code: 'not_found', message: completed.message })
        return answerJson(response, 400, { code: 'invalid_argument', message: completed.message })
      }
      if (completed.kind === 'pass') body = completed.body
      else {
        headers['content-length'] = String(completed.body.length)
        body = { chunks: [completed.body] }
      }
    }
    forward(request, response, target, headers, {
      body,
      timeouts: this.timeouts,
      fail: (reason) => this.log(`local proxy: Work ${workId} api upstream failed: ${reason}`),
    })
  }

  // A Storage content link the MCP route rewrote into a local handle: only a
  // handle issued to this Work reaches the platform's content endpoint, with
  // the query it stands for and the Work's identity that endpoint also asks
  // for. The handle stands in for the run token, which a model's curl lacks.
  private async content(request: IncomingMessage, response: ServerResponse, workId: string, rest: string): Promise<void> {
    const handle = CONTENT_HANDLE.exec(rest)?.[1]
    const query = handle === undefined ? undefined : this.contentHandles.query(workId, handle)
    if (query === undefined) return notFound(response)
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      return answerJson(response, 405, { code: 'METHOD_NOT_ALLOWED', message: 'content links are read with GET' })
    }
    const credentials = await this.access.credentials(workId)
    if (credentials === undefined) {
      return answerJson(response, 503, { code: 'CREDENTIALS_NOT_DELIVERED', message: 'the Work\'s credentials were not delivered' })
    }
    const { gateway } = credentials
    const headers = requestHeaders(request.headers)
    addIdentity(headers, gateway)
    forward(request, response, new URL(`${contentBase(gateway.url)}?${query}`), headers, {
      timeouts: this.timeouts,
      fail: (reason) => this.log(`local proxy: Work ${workId} content upstream failed: ${reason}`),
    })
  }
}

// The JSON-RPC response the proxy gives itself, or 202 when the message
// held no request to answer.
function answerRpc(response: ServerResponse, body: Buffer | undefined): void {
  if (body === undefined) return void response.writeHead(202).end()
  response.writeHead(200, { 'content-type': 'application/json', 'content-length': body.length }).end(body)
}

// The Work's Identity Token, which the platform's entry verifies on the
// Agent's calls; an Access Link on the content path takes none.
function addIdentityToken(headers: OutgoingHttpHeaders, gateway: WorkCredentials['gateway']): void {
  headers.authorization = `Bearer ${gateway.identityToken}`
}

// The Space Token of the Work's input source, a capability, not an identity.
function addSpaceToken(headers: OutgoingHttpHeaders, gateway: WorkCredentials['gateway']): void {
  if (gateway.spaceToken !== undefined) headers['x-talgent-space-token'] = gateway.spaceToken
}

// The Work's identity, as the platform's MCP and content endpoints take it.
function addIdentity(headers: OutgoingHttpHeaders, gateway: WorkCredentials['gateway']): void {
  headers['x-talgent-work-id'] = gateway.workId
  headers['x-talgent-work-credential'] = gateway.workCredential
  if (gateway.spaceToken !== undefined) headers['x-talgent-space-token'] = gateway.spaceToken
}

function notFound(response: ServerResponse): void {
  answerJson(response, 404, { code: 'NOT_FOUND', message: 'no such proxy route' })
}

// The upstream URL of rest under base. It stays below base's path: rest may
// not hold a backslash (URL parsers read it as a slash), an encoded slash,
// backslash or dot, or a . or .. segment.
function below(base: string, rest: string, search: string): URL | undefined {
  if (rest.includes('\\') || /%(2f|5c|2e)/i.test(rest)) return undefined
  if (rest.split('/').some((segment) => segment === '.' || segment === '..')) return undefined
  const root = new URL(base)
  const basePath = root.pathname.replace(/\/+$/, '')
  const target = new URL(root.origin + basePath + rest + search)
  if (target.origin !== root.origin) return undefined
  if (target.pathname !== basePath && !target.pathname.startsWith(basePath + '/')) return undefined
  return target
}

function presentedToken(headers: IncomingHttpHeaders): string {
  const bearer = /^Bearer\s+(\S+)\s*$/i.exec(headers.authorization ?? '')
  if (bearer !== null) return bearer[1]
  const key = headers['x-api-key']
  return typeof key === 'string' ? key.trim() : ''
}
