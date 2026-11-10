import type { IncomingMessage } from 'node:http'
import { isObject } from '../json.ts'
import { holdingCompletion, MAX_REQUEST_BYTES, readSource, UNHELD_REQUEST_BYTES } from './publication-source.ts'
import { readHead, type ReadBody } from './request-body.ts'

// Publication through the entry: CreateArtifact and ReplaceArtifact name a
// file of the Work by its Work Directory and path, which the platform cannot
// read on this machine. The local proxy alone sends the file's bytes as
// content in their place; a call it cannot complete is answered here as the
// API refuses one, and never forwarded.

export const ARTIFACT_COMMANDS = new Set([
  '/api/storage.public.v1.ArtifactService/CreateArtifact',
  '/api/storage.public.v1.ArtifactService/ReplaceArtifact',
])

// The request forwarded as read, forwarded completed, or answered here with
// a Connect error.
export type ApiCompletion =
  | { kind: 'pass'; body: ReadBody }
  | { kind: 'forward'; body: Buffer }
  | { kind: 'refuse'; message: string; missing: boolean }

// Completes a Connect JSON Artifact command naming a path. Anything else,
// such as one that already carries content, which is longer than a command
// naming a path, passes unchanged, and the API answers it. Only reading the
// file holds a completion slot.
export async function completeArtifactCommand(request: IncomingMessage, root: string): Promise<ApiCompletion> {
  const encoding = request.headers['content-encoding']
  if (encoding !== undefined && encoding !== 'identity') return { kind: 'pass', body: { chunks: [], rest: request } }
  const head = await readHead(request, UNHELD_REQUEST_BYTES)
  if (head.rest !== undefined) return { kind: 'pass', body: head }
  const body = Buffer.concat(head.chunks)
  let message: unknown
  try {
    message = JSON.parse(body.toString('utf8'))
  } catch {
    return { kind: 'pass', body: { chunks: [body] } }
  }
  if (!isObject(message) || typeof message.path !== 'string' || message.content !== undefined) return { kind: 'pass', body: { chunks: [body] } }
  // The Work Directory names where the file is; content sent names none. A
  // Managed Work has one directory, its Work root, whatever the Work names.
  const { path, work_directory: _directory, workDirectory: _camel, ...rest } = message
  return holdingCompletion(async () => {
    const source = await readSource(root, path, 'path', MAX_REQUEST_BYTES - body.length)
    if ('refusal' in source) return { kind: 'refuse', message: source.refusal, missing: source.missing }
    return { kind: 'forward', body: Buffer.from(JSON.stringify({ ...rest, content: source.content.toString('base64') })) }
  })
}
