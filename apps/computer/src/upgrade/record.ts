import { mkdirSync, readFileSync } from 'node:fs'
import { dirname } from 'node:path'
import { writeJsonAtomic } from '../execd/shim.ts'
import { isObject } from '../json.ts'

// What Talgent Computer keeps across its restarts about upgrading itself:
// the upgrade whose new version has yet to come online, and the releases
// that failed, each with when it may be tried again.

export interface PendingUpgrade {
  from: string
  to: string
  // The new version must come online by then, in Unix milliseconds.
  deadline: number
  // How often the new version started without coming online.
  starts: number
}

export interface FailedRelease {
  failures: number
  retryAt: number
}

export interface UpgradeRecord {
  pending?: PendingUpgrade
  failed: Record<string, FailedRelease>
}

// Reads the record; a missing or unreadable one is empty, so a damaged
// file never stops a run.
export function readRecord(file: string): UpgradeRecord {
  let parsed: unknown
  try {
    parsed = JSON.parse(readFileSync(file, 'utf8'))
  } catch {
    return { failed: {} }
  }
  if (!isObject(parsed)) return { failed: {} }
  const record: UpgradeRecord = { failed: {} }
  const pending = parsed.pending
  if (isObject(pending) && typeof pending.from === 'string' && typeof pending.to === 'string' && typeof pending.deadline === 'number' && typeof pending.starts === 'number') {
    record.pending = { from: pending.from, to: pending.to, deadline: pending.deadline, starts: pending.starts }
  }
  if (isObject(parsed.failed)) {
    for (const [release, value] of Object.entries(parsed.failed)) {
      if (isObject(value) && typeof value.failures === 'number' && typeof value.retryAt === 'number') record.failed[release] = { failures: value.failures, retryAt: value.retryAt }
    }
  }
  return record
}

export function writeRecord(file: string, record: UpgradeRecord): void {
  mkdirSync(dirname(file), { recursive: true, mode: 0o700 })
  writeJsonAtomic(file, record)
}
