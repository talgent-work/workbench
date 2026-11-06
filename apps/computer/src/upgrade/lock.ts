import { mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { isErrno } from '../store.ts'

// One Talgent Computer process at a time installs into the npm prefix and
// changes the upgrade record: a run replaced by another can still be
// finishing an install while the new run starts one. The lock is a file
// holding its owner's pid, created exclusively; a lock whose owner no
// longer runs, or that is older than any install may take, is stale and
// taken over.

export interface InstallLockOptions {
  file: string
  // Past this age a lock is stale even when its pid runs again, as a reused
  // pid may.
  staleAfterMs: number
  // How long acquiring waits for the owner to finish.
  waitMs: number
  now?: () => number
  alive?: (pid: number) => boolean
}

interface LockRecord {
  pid: number
  at: number
}

const POLL_MS = 250
const UNREADABLE_GRACE_MS = 5_000

export class InstallLock {
  private readonly options: InstallLockOptions
  private readonly now: () => number
  private readonly alive: (pid: number) => boolean

  constructor(options: InstallLockOptions) {
    this.options = options
    this.now = options.now ?? Date.now
    this.alive = options.alive ?? processAlive
  }

  // Runs task holding the lock; rejects when another process holds it
  // longer than waitMs.
  async hold<T>(task: () => Promise<T>): Promise<T> {
    const deadline = this.now() + this.options.waitMs
    while (!this.tryAcquire()) {
      if (this.now() >= deadline) throw new Error(`another Talgent Computer process is installing (${this.options.file})`)
      await delay(POLL_MS)
    }
    try {
      return await task()
    } finally {
      rmSync(this.options.file, { force: true })
    }
  }

  private tryAcquire(): boolean {
    const { file } = this.options
    mkdirSync(dirname(file), { recursive: true, mode: 0o700 })
    try {
      writeFileSync(file, JSON.stringify({ pid: process.pid, at: this.now() } satisfies LockRecord), { flag: 'wx', mode: 0o600 })
      return true
    } catch (error) {
      if (!isErrno(error, 'EEXIST')) throw error
    }
    const held = this.read()
    if (held === undefined) {
      // Being written by its owner right now, or left unreadable long ago.
      if (this.now() - modifiedAt(file) < UNREADABLE_GRACE_MS) return false
    } else if (this.alive(held.pid) && this.now() - held.at < this.options.staleAfterMs) {
      return false
    }
    // Stale or unreadable: removed, and created again
    // on the next try, which another process may win.
    rmSync(file, { force: true })
    return false
  }

  private read(): LockRecord | undefined {
    try {
      const parsed = JSON.parse(readFileSync(this.options.file, 'utf8')) as Partial<LockRecord>
      return typeof parsed.pid === 'number' && typeof parsed.at === 'number' ? { pid: parsed.pid, at: parsed.at } : undefined
    } catch {
      return undefined
    }
  }
}

function modifiedAt(file: string): number {
  try {
    return statSync(file).mtimeMs
  } catch {
    return 0
  }
}

function processAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch (error) {
    return isErrno(error, 'EPERM')
  }
}
