import { lstat, realpath } from 'node:fs/promises'
import { isAbsolute, join, normalize, relative } from 'node:path'
import { openWorkFile, readBounded } from '../execd/work-files.ts'
import { within, workspaceRelative } from '../execd/work-paths.ts'
import { Limit } from '../limit.ts'

// A publication's source: the file of the Work a publication names, which
// the platform cannot read on this machine, read by the local proxy for its
// MCP route (publication.ts) and its native API route (api-publication.ts)
// alike, within the platform's bounds on one request.

export const MAX_SOURCE_BYTES = 64 * 1024 * 1024
// The platform's bound on one publishing request: a source's base64 and
// 1 MiB besides. The proxy reads a request up to it; a longer one passes
// unchanged and the platform refuses it.
export const MAX_REQUEST_BYTES = base64Length(MAX_SOURCE_BYTES) + 1024 * 1024
// A request body is read this far before it waits for a completion slot.
export const UNHELD_REQUEST_BYTES = 1024 * 1024
// The Work's read-only inputs and Project Knowledge, which a publication
// never reads from.
const RESERVED_DIRS = ['inputs', 'knowledge']

// Requests completed at a time, over both routes, each holding up to a
// request's worth of files, or of body read past UNHELD_REQUEST_BYTES.
const completions = new Limit(2)

// Runs task holding a completion slot.
export function holdingCompletion<T>(task: () => Promise<T>): Promise<T> {
  return completions.run(task)
}

const noFile = (field: string) => `${field} names no file in the Work's workspace.`
const notOneFile = (field: string) => `${field} is not one regular file in the Work's workspace, reached without symbolic links.`
const TOO_LARGE = 'The source file is larger than 64 MiB. Split it or compress it into a smaller archive.'
const reserved = (field: string, dir: string) => `${field} is under ${dir}/, which the Work only reads. Publish a file outside inputs/ and knowledge/.`
const OVER_REQUEST = 'The files published in this request exceed its size limit. Publish them in separate requests.'

// Why a source is refused: missing names no file at all; anything else is
// a source the platform does not publish.
export type SourceRefusal = { refusal: string; missing: boolean }

// The bytes of the regular file sourcePath, the request's field, names under
// the Work root, or why it is refused. As on a Cloud, sourcePath is
// /workspace/..., relative to the Work root, or here also an absolute path
// into it on this machine; it is reached without symbolic links and lies
// outside the Work's inputs/ and knowledge/. Its base64 fits in budget bytes.
export async function readSource(root: string, sourcePath: string, field: string, budget: number): Promise<{ content: Buffer } | SourceRefusal> {
  const invalid = (refusal: string): SourceRefusal => ({ refusal, missing: false })
  if (sourcePath === '' || sourcePath.includes('\0')) return invalid(notOneFile(field))
  let rootReal: string
  try {
    rootReal = await realpath(root)
  } catch {
    return invalid(notOneFile(field))
  }
  const target = workspaceRelative(sourcePath) ?? sourcePath
  const named = isAbsolute(target) ? normalize(target) : join(rootReal, target)
  const lexical = within(root, named) ? join(rootReal, relative(root, named)) : named
  if (!within(rootReal, lexical) || lexical === rootReal) return invalid(notOneFile(field))
  const file = await openWorkFile(root, lexical).catch(() => ({ status: 500 }))
  if ('status' in file) {
    const missing = file.status === 404 && (await absent(lexical))
    return missing ? { refusal: noFile(field), missing: true } : invalid(notOneFile(field))
  }
  try {
    // The file reached is the one named, with no symbolic link on the way.
    if (file.path !== lexical) return invalid(notOneFile(field))
    for (const dir of RESERVED_DIRS) {
      if (within(join(rootReal, dir), file.path)) return invalid(reserved(field, dir))
    }
    if (file.size > MAX_SOURCE_BYTES) return invalid(TOO_LARGE)
    if (base64Length(file.size) > budget) return invalid(OVER_REQUEST)
    const content = await readBounded(file.handle, MAX_SOURCE_BYTES)
    if (content === undefined) return invalid(TOO_LARGE)
    if (base64Length(content.length) > budget) return invalid(OVER_REQUEST)
    return { content }
  } catch {
    return invalid(notOneFile(field))
  } finally {
    await file.handle.close().catch(() => {})
  }
}

// Whether nothing is at path, not even a dangling link.
async function absent(path: string): Promise<boolean> {
  try {
    await lstat(path)
    return false
  } catch {
    return true
  }
}

export function base64Length(bytes: number): number {
  return 4 * Math.ceil(bytes / 3)
}
