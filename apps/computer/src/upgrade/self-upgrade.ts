import { COMPUTER_PACKAGE } from './installation.ts'
import type { Installer } from './npm.ts'
import { readRecord, writeRecord, type UpgradeRecord } from './record.ts'

// Talgent Computer runs the Target Version the platform announces on every
// connection. When the Target is a release other than its own, it installs that
// release with npm, checks that the installed command answers it, and restarts
// into it; Work processes run in their own shims and continue, and the next run
// picks them up. The new version must come online within a bound, or it
// reinstalls the version it came from and restarts into that. A release that
// failed is tried again only after a backoff, so a bad release never loops.
// Below the Minimum Version the platform admits no new run here.

// A platform release as Auto Tag numbers it; any other Target is no release.
const RELEASE = /^(\d+)\.(\d+)\.(\d+)$/

export const PROBATION_MS = 3 * 60_000
// A new version that started this often without coming online fails.
export const MAX_PROBATION_STARTS = 3
export const RETRY_BASE_MS = 15 * 60_000
export const RETRY_MAX_MS = 6 * 60 * 60_000

export interface Announcement {
  target: string
  minimum: string
}

export interface SelfUpgradeOptions {
  version: string
  recordFile: string
  // How it installs a release, or why it cannot upgrade itself.
  installer: Installer | { unavailable: string }
  // Held around every install and change of the record (lock.ts).
  lock: { hold<T>(task: () => Promise<T>): Promise<T> }
  // Ends this run so that the installed version runs next.
  restart: () => void
  log: (message: string) => void
  now?: () => number
  probationMs?: number
}

export class SelfUpgrade {
  private readonly options: SelfUpgradeOptions
  private readonly now: () => number
  private probation: NodeJS.Timeout | undefined
  // An upgrade or a rollback in progress; a stopping run waits for it.
  private upgrading: Promise<void> | undefined
  private rollingBack: Promise<unknown> | undefined
  private readonly told = new Set<string>()

  constructor(options: SelfUpgradeOptions) {
    this.options = options
    this.now = options.now ?? Date.now
  }

  // At the start of a run: a new version on probation that already started
  // too often, or past its deadline, goes back to the version it came from
  // and answers "restart"; otherwise its deadline is watched from now.
  async start(): Promise<'run' | 'restart'> {
    const verdict = await this.options.lock.hold(async (): Promise<'run' | string | { deadline: number }> => {
      const record = readRecord(this.options.recordFile)
      const pending = record.pending
      if (pending === undefined) return 'run'
      if (pending.to !== this.options.version) {
        // Another version is running: the upgrade was rolled back or the
        // person installed one themselves.
        delete record.pending
        writeRecord(this.options.recordFile, record)
        return 'run'
      }
      pending.starts += 1
      writeRecord(this.options.recordFile, record)
      if (pending.starts > MAX_PROBATION_STARTS || this.now() > pending.deadline) return `it started ${pending.starts} times without coming online`
      return { deadline: pending.deadline }
    }).catch((error: unknown) => {
      // The record stays for the next run to read.
      this.options.log(`cannot read the upgrade record: ${message(error)}`)
      return 'run' as const
    })
    if (verdict === 'run') return 'run'
    if (typeof verdict === 'string') return await this.rollBack(verdict, this.options.version) ? 'restart' : 'run'
    this.probation = setTimeout(() => {
      this.probation = undefined
      void this.track(this.rollBack('it did not come online in time', this.options.version)).then((restart) => { if (restart) this.options.restart() })
    }, Math.max(0, verdict.deadline - this.now()))
    this.probation.unref?.()
    return 'run'
  }

  // A run that cannot start: on probation it goes back to the previous
  // version and answers true, so the caller restarts into that.
  async failedToStart(reason: string): Promise<boolean> {
    return await this.track(this.rollBack(reason, this.options.version))
  }

  // A supervisor's run exited with a failure: when it ran a version on
  // probation, which may have failed before it could watch its own
  // probation, that version goes back to the one it came from, and true
  // answers that the supervisor restarts into it.
  async runFailed(reason: string): Promise<boolean> {
    return await this.track(this.rollBack(reason))
  }

  // The platform's announcement on a connection: being online confirms a
  // new version, and a Target other than this version is installed.
  announced(announcement: Announcement): void {
    this.confirm()
    const { version, log } = this.options
    if (below(version, announcement.minimum)) {
      log(`Talgent Computer ${version} is below the Minimum Version ${announcement.minimum}; this Computer takes no new run until it upgrades`)
    }
    const target = announcement.target
    if (!RELEASE.test(target) || target === version || this.upgrading !== undefined || this.rollingBack !== undefined) return
    if (!('install' in this.options.installer)) {
      this.tellOnce(`unavailable ${target}`, `Talgent ${target} is out; this Talgent Computer ${version} cannot upgrade itself because ${this.options.installer.unavailable}. Run "npm install -g ${COMPUTER_PACKAGE}@${target}"`)
      return
    }
    const failed = readRecord(this.options.recordFile).failed[target]
    if (failed !== undefined && this.now() < failed.retryAt) {
      this.tellOnce(`retry ${target} ${failed.retryAt}`, `upgrading to ${target} failed before; trying again after ${new Date(failed.retryAt).toISOString()}`)
      return
    }
    const installer = this.options.installer
    this.upgrading = this.options.lock.hold(() => this.upgrade(installer, target))
      .catch((error: unknown) => log(`upgrading to ${target} failed: ${message(error)}`))
      .finally(() => { this.upgrading = undefined })
  }

  // Waits for an upgrade or a rollback in progress, so a stopping run never
  // leaves an install half done.
  async settle(): Promise<void> {
    clearTimeout(this.probation)
    this.probation = undefined
    await this.upgrading
    await this.rollingBack
  }

  private track<T>(work: Promise<T>): Promise<T> {
    const tracked = work.finally(() => { if (this.rollingBack === tracked) this.rollingBack = undefined })
    this.rollingBack = tracked
    return tracked
  }

  private async upgrade(installer: Installer, target: string): Promise<void> {
    const { version, log } = this.options
    log(`upgrading Talgent Computer ${version} to ${target}; running Works continue`)
    try {
      await installVerified(installer, target)
    } catch (error) {
      log(`upgrading to ${target} failed: ${message(error)}`)
      this.recordFailure(readRecord(this.options.recordFile), target)
      await this.restore(installer)
      return
    }
    const record = readRecord(this.options.recordFile)
    record.pending = { from: version, to: target, deadline: this.now() + (this.options.probationMs ?? PROBATION_MS), starts: 0 }
    writeRecord(this.options.recordFile, record)
    log(`installed Talgent Computer ${target}; restarting into it`)
    this.options.restart()
  }

  // Puts this version back unless the installed command still answers it.
  private async restore(installer: Installer): Promise<void> {
    const { version, log } = this.options
    if (await installer.installedVersion().catch(() => '') === version) return
    log(`reinstalling Talgent Computer ${version}`)
    try {
      await installVerified(installer, version)
    } catch (error) {
      log(`reinstalling Talgent Computer ${version} failed: ${message(error)}; run "npm install -g ${COMPUTER_PACKAGE}@${version}"`)
    }
  }

  // A new version that failed its probation reinstalls the one it came
  // from; true when that one is installed to restart into. Only the version
  // on probation is rolled back when running names one.
  private async rollBack(reason: string, running?: string): Promise<boolean> {
    clearTimeout(this.probation)
    this.probation = undefined
    const { log, installer, recordFile } = this.options
    const rolledBack = this.options.lock.hold(async () => {
      const record = readRecord(recordFile)
      const pending = record.pending
      if (pending === undefined || (running !== undefined && pending.to !== running)) return false
      log(`Talgent Computer ${pending.to} failed: ${reason}; going back to ${pending.from}`)
      delete record.pending
      this.recordFailure(record, pending.to)
      if (!('install' in installer)) return false
      try {
        await installVerified(installer, pending.from)
        return true
      } catch (error) {
        log(`reinstalling Talgent Computer ${pending.from} failed: ${message(error)}; staying on ${pending.to}`)
        return false
      }
    })
    return await rolledBack.catch((error: unknown) => {
      log(`cannot go back to the previous version now: ${message(error)}`)
      return false
    })
  }

  private confirm(): void {
    clearTimeout(this.probation)
    this.probation = undefined
    // Too late: a rollback already began.
    if (this.rollingBack !== undefined) return
    const record = readRecord(this.options.recordFile)
    if (record.pending?.to !== this.options.version) return
    delete record.pending
    delete record.failed[this.options.version]
    writeRecord(this.options.recordFile, record)
    this.options.log(`upgraded to Talgent Computer ${this.options.version}`)
  }

  private recordFailure(record: UpgradeRecord, release: string): void {
    const failures = (record.failed[release]?.failures ?? 0) + 1
    record.failed[release] = { failures, retryAt: this.now() + Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** (failures - 1)) }
    writeRecord(this.options.recordFile, record)
  }

  private tellOnce(key: string, line: string): void {
    if (this.told.has(key)) return
    this.told.add(key)
    this.options.log(line)
  }
}

// Installs a release and checks that the installed command answers it.
async function installVerified(installer: Installer, version: string): Promise<void> {
  await installer.install(version)
  const installed = await installer.installedVersion()
  if (installed !== version) throw new Error(`the installed talgent-computer answers ${JSON.stringify(installed)}, not ${version}`)
}

// Whether a release is below a minimum release; no minimum, or one that is
// no release, is below nothing.
export function below(version: string, minimum: string): boolean {
  const want = RELEASE.exec(minimum)
  if (want === null) return false
  const have = RELEASE.exec(version)
  if (have === null) return true
  for (let i = 1; i <= 3; i += 1) {
    if (Number(have[i]) !== Number(want[i])) return Number(have[i]) < Number(want[i])
  }
  return false
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
