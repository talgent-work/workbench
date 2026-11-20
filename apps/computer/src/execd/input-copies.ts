import { chmod, lstat, readdir, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { isErrno } from '../store.ts'
import { validId } from './works.ts'

// Earlier releases kept copies of a Work's inputs in its inputs/ directory,
// each file and the directory read-only, and a download record per Work
// under recordsDir. A Work's Agent now downloads the inputs it needs itself,
// and an old copy would read as current, so the first start after the
// upgrade clears the inputs/ of each recorded Work once: it removes every
// read-only file, which only those releases left there, and any download
// they left unfinished, makes inputs/ writable, then deletes the record.
// Records gone, later starts never touch inputs/. A start cut short goes on
// at the next one, whose read-only test still spares what an Agent wrote. A
// copy that cannot be removed is logged and left for the person; it is not
// tried again.

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
  for (const name of names) {
    const workId = name.endsWith('.json') ? name.slice(0, -'.json'.length) : ''
    if (validId(workId)) await clearInputs(join(options.workRoot(workId), 'inputs'), (message) => log(`Work ${workId} inputs: ${message}`))
    await rm(join(recordsDir, name), { force: true })
  }
  await rm(recordsDir, { recursive: true, force: true })
}

// Removes the copies and unfinished downloads from dir, then lets the Work
// write there; anything else in dir stays.
async function clearInputs(dir: string, log: (message: string) => void): Promise<void> {
  try {
    if (!(await lstat(dir)).isDirectory()) return
    await chmod(dir, 0o755)
  } catch (error) {
    if (isErrno(error, 'ENOENT')) return
    return log(`cannot open inputs/ to the Work: ${messageOf(error)}`)
  }
  let removed = 0
  for (const name of await readdir(dir)) {
    const path = join(dir, name)
    try {
      const info = await lstat(path)
      if (!info.isFile() || (!name.endsWith(PARTIAL_DOWNLOAD) && (info.mode & 0o222) !== 0)) continue
      // Windows refuses to remove a read-only file.
      await chmod(path, 0o644)
      await rm(path, { force: true })
      removed += 1
    } catch (error) {
      log(`cannot remove ${JSON.stringify(name)}, an outdated copy an earlier release downloaded; remove it by hand: ${messageOf(error)}`)
    }
  }
  if (removed > 0) log(`removed ${removed} outdated copies an earlier release downloaded`)
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
