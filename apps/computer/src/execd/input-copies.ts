import { chmod, lstat, readdir, readFile, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { isObject } from '../json.ts'
import { isErrno } from '../store.ts'
import { validId } from './works.ts'

// Earlier releases kept read-only copies of each Work's inputs in its
// inputs/ directory and recorded each Work's downloads under recordsDir. A
// Work's Agent now downloads the inputs it needs itself, and an old copy
// would read as current, so the first start after the upgrade removes every
// recorded copy, any download left unfinished and the record, and gives
// inputs/ back to the Work. Records gone, later starts find nothing to do
// and never touch inputs/. A Work whose cleanup fails keeps its record, so
// the next start finishes it.

// The suffix of a download the earlier releases had not finished.
const PARTIAL_DOWNLOAD = '.talgent-download'

export interface InputCopies {
  // The earlier releases' download records, one <workId>.json per Work.
  recordsDir: string
  workRoot: (workId: string) => string
  log: (message: string) => void
}

export async function removeInputCopies(options: InputCopies): Promise<void> {
  const { recordsDir, log } = options
  let names: string[]
  try {
    names = await readdir(recordsDir)
  } catch (error) {
    if (isErrno(error, 'ENOENT')) return
    throw error
  }
  let failed = false
  for (const name of names) {
    const workId = name.endsWith('.json') ? name.slice(0, -'.json'.length) : ''
    const record = join(recordsDir, name)
    try {
      if (validId(workId)) await removeCopies(join(options.workRoot(workId), 'inputs'), await recordedNames(record))
      await rm(record, { force: true })
    } catch (error) {
      failed = true
      log(`Work ${workId} inputs: cannot remove the copies earlier releases downloaded, the next start tries again: ${reasonOf(error)}`)
    }
  }
  if (!failed) await rm(recordsDir, { recursive: true, force: true })
}

async function recordedNames(record: string): Promise<string[]> {
  try {
    const parsed = JSON.parse(await readFile(record, 'utf8')) as unknown
    return isObject(parsed) ? Object.keys(parsed) : []
  } catch (error) {
    // A record cut short by a crash names nothing.
    if (error instanceof SyntaxError) return []
    throw error
  }
}

// Removes the recorded copies and unfinished downloads from dir, then lets
// the Work write there; anything else in dir stays.
async function removeCopies(dir: string, recorded: string[]): Promise<void> {
  try {
    if (!(await lstat(dir)).isDirectory()) return
  } catch (error) {
    if (isErrno(error, 'ENOENT')) return
    throw error
  }
  // The earlier releases left dir read-only.
  await chmod(dir, 0o755)
  const copies = new Set(recorded)
  for (const name of await readdir(dir)) {
    if (!copies.has(name) && !name.endsWith(PARTIAL_DOWNLOAD)) continue
    const path = join(dir, name)
    // Only a file is a copy: a directory or link of that name was placed by someone else.
    if (!(await lstat(path)).isFile()) continue
    // Windows refuses to remove a read-only file.
    await chmod(path, 0o644)
    await rm(path, { force: true })
  }
}

function reasonOf(error: unknown): string {
  const code = (error as NodeJS.ErrnoException | undefined)?.code
  return typeof code === 'string' ? code : 'failed'
}
