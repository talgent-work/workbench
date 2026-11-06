import { constants, type Stats } from 'node:fs'
import { open, readdir, realpath, stat, type FileHandle } from 'node:fs/promises'
import { basename, isAbsolute, join } from 'node:path'
import { isErrno } from '../store.ts'
import type { PipeSink } from './pipe.ts'
import { within } from './work-paths.ts'

// A Work's files as the platform browses them and as the local proxy reads them
// for publication. Every path stays under the Work root's real path, symbolic
// links resolved: one that leaves it reads as missing.

// The bytes of one binary frame of a file read.
export const FILE_CHUNK_BYTES = 64 * 1024
// Directory entries stat'ed at a time.
const STAT_BATCH = 32
// Opening never blocks on a FIFO swapped in after the check, nor follows a
// link swapped in for the last component.
const READ_FLAGS = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK

export interface FileEntry {
  name: string
  kind: 'file' | 'directory'
  size: number
  modified_at: string
}

// The real path target names under root, when it exists and stays under
// root's real path. A relative target is taken from root; an absolute one is
// a path of this machine.
export async function confined(root: string, target: string): Promise<string | undefined> {
  if (target.includes('\0')) return undefined
  try {
    const rootReal = await realpath(root)
    const real = await realpath(isAbsolute(target) ? target : join(rootReal, target))
    return within(rootReal, real) ? real : undefined
  } catch (error) {
    if (unreachable(error)) return undefined
    throw error
  }
}

function unreachable(error: unknown): boolean {
  return ['ENOENT', 'ENOTDIR', 'ELOOP', 'EACCES', 'ENAMETOOLONG', 'EINVAL'].some((code) => isErrno(error, code))
}

// A path of the Work files API: relative to the Work root, "" being the root.
function apiPath(path: string): string {
  return path.replace(/^\/+/, '')
}

export type Listing = { kind: 'ok'; entries: FileEntry[] } | { kind: 'missing' } | { kind: 'not_directory' }

// The files and directories of one directory of the Work root, by name. An
// entry that is neither, or a link leading out of the root, is left out.
export async function listDirectory(root: string, path: string): Promise<Listing> {
  const dir = await confined(root, apiPath(path))
  if (dir === undefined) return { kind: 'missing' }
  let names: string[]
  try {
    names = await readdir(dir)
  } catch (error) {
    if (isErrno(error, 'ENOTDIR')) return { kind: 'not_directory' }
    if (unreachable(error)) return { kind: 'missing' }
    throw error
  }
  names.sort()
  const entries: FileEntry[] = []
  for (let start = 0; start < names.length; start += STAT_BATCH) {
    const batch = await Promise.all(names.slice(start, start + STAT_BATCH).map((name) => entryOf(root, dir, name)))
    for (const entry of batch) if (entry !== undefined) entries.push(entry)
  }
  return { kind: 'ok', entries }
}

// One file or directory of the Work root, named by its last component; a
// path out of the root, through a link or missing, or naming neither, has
// none.
export async function statEntry(root: string, path: string): Promise<FileEntry | undefined> {
  const target = apiPath(path)
  const real = await confined(root, target)
  return real === undefined ? undefined : describe(basename(target), real)
}

async function entryOf(root: string, dir: string, name: string): Promise<FileEntry | undefined> {
  const real = await confined(root, join(dir, name))
  return real === undefined ? undefined : describe(name, real)
}

async function describe(name: string, real: string): Promise<FileEntry | undefined> {
  let info: Stats
  try {
    info = await stat(real)
  } catch (error) {
    if (unreachable(error)) return undefined
    throw error
  }
  if (!info.isFile() && !info.isDirectory()) return undefined
  return {
    name,
    kind: info.isFile() ? 'file' : 'directory',
    size: info.isFile() ? info.size : 0,
    modified_at: info.mtime.toISOString(),
  }
}

// A regular file of the Work open for reading, by its real path.
export interface OpenedFile {
  handle: FileHandle
  size: number
  path: string
}

// Opens a regular file under root for reading, or answers why not: 404
// missing or outside the root, 400 a directory.
export async function openWorkFile(root: string, target: string): Promise<{ status: number } | OpenedFile> {
  const real = await confined(root, target)
  if (real === undefined) return { status: 404 }
  let info: Stats
  try {
    info = await stat(real)
  } catch (error) {
    if (unreachable(error)) return { status: 404 }
    throw error
  }
  if (info.isDirectory()) return { status: 400 }
  if (!info.isFile()) return { status: 404 }
  let handle: FileHandle
  try {
    handle = await open(real, READ_FLAGS)
  } catch (error) {
    if (unreachable(error)) return { status: 404 }
    throw error
  }
  const opened = await handle.stat()
  if (!opened.isFile()) {
    await handle.close()
    return { status: 404 }
  }
  return { handle, size: opened.size, path: real }
}

export function openApiFile(root: string, path: string): Promise<{ status: number } | OpenedFile> {
  return openWorkFile(root, apiPath(path))
}

// Sends the file as binary frames of raw bytes while the link takes them,
// then ends the stream; a read failure ends it with 500 instead.
export async function streamFile(handle: FileHandle, sink: PipeSink, log: (message: string) => void): Promise<void> {
  try {
    for (;;) {
      await sink.writable()
      if (sink.closed.aborted) return
      const chunk = Buffer.allocUnsafe(FILE_CHUNK_BYTES)
      const { bytesRead } = await handle.read(chunk, 0, FILE_CHUNK_BYTES, null)
      if (sink.closed.aborted) return
      if (bytesRead === 0) return sink.end()
      sink.binary(chunk.subarray(0, bytesRead))
    }
  } catch (error) {
    log(`file read failed: ${(error as NodeJS.ErrnoException | undefined)?.code ?? 'read error'}`)
    sink.end(500)
  } finally {
    await handle.close().catch(() => {})
  }
}

// Reads at most max bytes of an open file; undefined when it holds more.
export async function readBounded(handle: FileHandle, max: number): Promise<Buffer | undefined> {
  const chunks: Buffer[] = []
  let total = 0
  for (;;) {
    const chunk = Buffer.allocUnsafe(Math.min(FILE_CHUNK_BYTES * 16, max + 1 - total))
    const { bytesRead } = await handle.read(chunk, 0, chunk.length, null)
    if (bytesRead === 0) return Buffer.concat(chunks, total)
    total += bytesRead
    if (total > max) return undefined
    chunks.push(chunk.subarray(0, bytesRead))
  }
}
