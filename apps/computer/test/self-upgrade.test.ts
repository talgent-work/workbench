import assert from 'node:assert/strict'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, test } from 'node:test'
import type { Installer } from '../src/upgrade/npm.ts'
import { InstallLock } from '../src/upgrade/lock.ts'
import { readRecord, writeRecord } from '../src/upgrade/record.ts'
import { below, MAX_PROBATION_STARTS, PROBATION_MS, RETRY_BASE_MS, SelfUpgrade } from '../src/upgrade/self-upgrade.ts'

const dirs: string[] = []
afterEach(async () => {
  while (dirs.length > 0) await rm(dirs.pop() ?? '', { recursive: true, force: true })
})

// An npm stand-in: what is installed, which releases its install refuses,
// and what the installed command answers.
class FakeInstaller implements Installer {
  installs: string[] = []
  refuse = new Set<string>()
  // A release whose installed command answers another version.
  answers = new Map<string, string>()
  installed: string
  // How long each install takes, and the most installs at once.
  takesMs = 0
  private active = 0
  mostAtOnce = 0
  constructor(installed: string) {
    this.installed = installed
  }

  async install(version: string): Promise<void> {
    this.installs.push(version)
    this.active += 1
    this.mostAtOnce = Math.max(this.mostAtOnce, this.active)
    await new Promise((resolve) => setTimeout(resolve, this.takesMs))
    this.active -= 1
    if (this.refuse.has(version)) throw new Error('npm error code EINTEGRITY')
    this.installed = version
  }

  async installedVersion(): Promise<string> {
    return this.answers.get(this.installed) ?? this.installed
  }
}

async function setup(version: string, installer: FakeInstaller | { unavailable: string } = new FakeInstaller(version), shared?: string) {
  const dir = shared ?? await mkdtemp(join(tmpdir(), 'self-upgrade-'))
  if (shared === undefined) dirs.push(dir)
  const recordFile = join(dir, 'state', 'upgrade.json')
  const lock = new InstallLock({ file: join(dir, 'state', 'upgrade.lock'), staleAfterMs: 60_000, waitMs: 10_000 })
  const logs: string[] = []
  const clock = { now: 1_000_000 }
  let restarts = 0
  const upgrade = new SelfUpgrade({ version, recordFile, installer, lock, restart: () => { restarts += 1 }, log: (line) => logs.push(line), now: () => clock.now })
  return { upgrade, recordFile, logs, clock, dir, restarts: () => restarts }
}

test('a different release as Target is installed, checked and restarted into, on probation', async () => {
  const npm = new FakeInstaller('0.1.0')
  const { upgrade, recordFile, restarts, clock } = await setup('0.1.0', npm)
  for (const target of ['', '0.1.0', 'dev-123', '1.0.0-rc.1']) upgrade.announced({ target, minimum: '' })
  await upgrade.settle()
  assert.deepEqual(npm.installs, [], 'its own version or no release installs nothing')

  upgrade.announced({ target: '0.2.0', minimum: '' })
  upgrade.announced({ target: '0.2.0', minimum: '' })
  await upgrade.settle()
  assert.deepEqual(npm.installs, ['0.2.0'], 'one upgrade at a time')
  assert.equal(restarts(), 1)
  assert.deepEqual(readRecord(recordFile).pending, { from: '0.1.0', to: '0.2.0', deadline: clock.now + PROBATION_MS, starts: 0 })
})

test('a refused install keeps the previous version and is not retried before its backoff', async () => {
  const npm = new FakeInstaller('0.1.0')
  npm.refuse.add('0.2.0')
  const { upgrade, recordFile, restarts, clock, logs } = await setup('0.1.0', npm)
  upgrade.announced({ target: '0.2.0', minimum: '' })
  await upgrade.settle()
  assert.equal(restarts(), 0)
  assert.equal(npm.installed, '0.1.0')
  assert.deepEqual(npm.installs, ['0.2.0'], 'the untouched installation needs no reinstall')
  assert.match(logs.join('\n'), /upgrading to 0\.2\.0 failed: npm error code EINTEGRITY/)
  assert.deepEqual(readRecord(recordFile).failed['0.2.0'], { failures: 1, retryAt: clock.now + RETRY_BASE_MS })

  upgrade.announced({ target: '0.2.0', minimum: '' })
  await upgrade.settle()
  assert.deepEqual(npm.installs, ['0.2.0'], 'no loop on a reconnect')
  clock.now += RETRY_BASE_MS
  upgrade.announced({ target: '0.2.0', minimum: '' })
  await upgrade.settle()
  assert.deepEqual(npm.installs, ['0.2.0', '0.2.0'])
  assert.deepEqual(readRecord(recordFile).failed['0.2.0'], { failures: 2, retryAt: clock.now + 2 * RETRY_BASE_MS }, 'each failure doubles the backoff')
})

test('an installed command that answers another version is replaced by the previous one', async () => {
  const npm = new FakeInstaller('0.1.0')
  npm.answers.set('0.2.0', '0.1.9')
  const { upgrade, restarts } = await setup('0.1.0', npm)
  upgrade.announced({ target: '0.2.0', minimum: '' })
  await upgrade.settle()
  assert.equal(restarts(), 0)
  assert.deepEqual(npm.installs, ['0.2.0', '0.1.0'])
  assert.equal(npm.installed, '0.1.0')
})

test('a new version that comes online is confirmed', async () => {
  const { upgrade, recordFile, logs } = await setup('0.2.0')
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: 2_000_000, starts: 0 }, failed: { '0.2.0': { failures: 1, retryAt: 0 } } })
  assert.equal(await upgrade.start(), 'run')
  assert.equal(readRecord(recordFile).pending?.starts, 1)
  upgrade.announced({ target: '0.2.0', minimum: '' })
  await upgrade.settle()
  assert.deepEqual(readRecord(recordFile), { failed: {} })
  assert.ok(logs.includes('upgraded to Talgent Computer 0.2.0'))
})

test('a new version that does not come online in time goes back to the previous one and restarts', async () => {
  const npm = new FakeInstaller('0.2.0')
  const { upgrade, recordFile, restarts, clock } = await setup('0.2.0', npm)
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: clock.now + 20, starts: 0 }, failed: {} })
  assert.equal(await upgrade.start(), 'run')
  // The deadline is a real timer: on a busy runner it and the rollback after
  // it take longer than their 20 ms, so wait for the restart they end in.
  for (const giveUp = Date.now() + 5000; restarts() === 0 && Date.now() < giveUp;) await new Promise((resolve) => setTimeout(resolve, 10))
  assert.deepEqual(npm.installs, ['0.1.0'])
  assert.equal(restarts(), 1)
  const record = readRecord(recordFile)
  assert.equal(record.pending, undefined)
  assert.equal(record.failed['0.2.0']?.failures, 1, 'the previous version does not upgrade again at once')
})

test('a stopping run waits for a rollback its probation started', async () => {
  const npm = new FakeInstaller('0.2.0')
  npm.takesMs = 100
  const { upgrade, recordFile, clock, restarts } = await setup('0.2.0', npm)
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: clock.now + 10, starts: 0 }, failed: {} })
  assert.equal(await upgrade.start(), 'run')
  await new Promise((resolve) => setTimeout(resolve, 40))
  assert.deepEqual(npm.installs, ['0.1.0'], 'the rollback began')
  await upgrade.settle()
  assert.equal(npm.installed, '0.1.0', 'settled only once the reinstall finished')
  assert.equal(restarts(), 1)
})

test('a supervised run that failed on probation is rolled back by its supervisor', async () => {
  const npm = new FakeInstaller('0.2.0')
  // The supervisor runs the version the upgrade came from.
  const { upgrade, recordFile } = await setup('0.1.0', npm)
  assert.equal(await upgrade.runFailed('it exited with code 1'), false, 'no upgrade on probation')
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: Number.MAX_SAFE_INTEGER, starts: 1 }, failed: {} })
  assert.equal(await upgrade.runFailed('it exited with code 1'), true)
  assert.deepEqual(npm.installs, ['0.1.0'])
  const record = readRecord(recordFile)
  assert.equal(record.pending, undefined)
  assert.equal(record.failed['0.2.0']?.failures, 1)
})

test('two runs never install at once', async () => {
  const npm = new FakeInstaller('0.1.0')
  npm.takesMs = 100
  const first = await setup('0.1.0', npm)
  // A replaced run still finishing its upgrade, and the run that replaced it.
  const second = await setup('0.1.0', npm, first.dir)
  first.upgrade.announced({ target: '0.2.0', minimum: '' })
  second.upgrade.announced({ target: '0.2.0', minimum: '' })
  await Promise.all([first.upgrade.settle(), second.upgrade.settle()])
  assert.deepEqual(npm.installs, ['0.2.0', '0.2.0'])
  assert.equal(npm.mostAtOnce, 1)
})

test('a lock another process keeps neither stops a run nor installs past it', async () => {
  const { recordFile } = await setup('0.2.0')
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: Number.MAX_SAFE_INTEGER, starts: 0 }, failed: {} })
  const npm = new FakeInstaller('0.2.0')
  const logs: string[] = []
  const busy = { hold: async () => { throw new Error('another Talgent Computer process is installing') } }
  const upgrade = new SelfUpgrade({ version: '0.2.0', recordFile, installer: npm, lock: busy, restart: () => {}, log: (line) => logs.push(line) })
  assert.equal(await upgrade.start(), 'run')
  assert.equal(await upgrade.failedToStart('Claude Code does not run'), false)
  upgrade.announced({ target: '0.3.0', minimum: '' })
  await upgrade.settle()
  assert.deepEqual(npm.installs, [])
  for (const said of [/^cannot read the upgrade record/, /^cannot go back to the previous version now/, /^upgrading to 0\.3\.0 failed: another Talgent Computer process is installing/]) {
    assert.ok(logs.some((line) => said.test(line)), `${said} in ${logs.join('\n')}`)
  }
})

test('a new version that keeps failing to start goes back before it runs', async () => {
  const npm = new FakeInstaller('0.2.0')
  const { upgrade, recordFile } = await setup('0.2.0', npm)
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: Number.MAX_SAFE_INTEGER, starts: MAX_PROBATION_STARTS }, failed: {} })
  assert.equal(await upgrade.start(), 'restart')
  assert.deepEqual(npm.installs, ['0.1.0'])

  const again = await setup('0.2.0', new FakeInstaller('0.2.0'))
  writeRecord(again.recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: Number.MAX_SAFE_INTEGER, starts: 0 }, failed: {} })
  assert.equal(await again.upgrade.start(), 'run')
  assert.equal(await again.upgrade.failedToStart('Claude Code does not run'), true)
  assert.equal(await again.upgrade.failedToStart('Claude Code does not run'), false, 'only on probation')
})

test('a failed rollback stays on the running version', async () => {
  const npm = new FakeInstaller('0.2.0')
  npm.refuse.add('0.1.0')
  const { upgrade, recordFile, logs } = await setup('0.2.0', npm)
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: 0, starts: 0 }, failed: {} })
  assert.equal(await upgrade.start(), 'run')
  assert.equal(npm.installed, '0.2.0')
  assert.match(logs.join('\n'), /reinstalling Talgent Computer 0\.1\.0 failed.*staying on 0\.2\.0/)
})

test('a record of another version is dropped', async () => {
  const { upgrade, recordFile } = await setup('0.1.0')
  writeRecord(recordFile, { pending: { from: '0.1.0', to: '0.2.0', deadline: 0, starts: 0 }, failed: {} })
  assert.equal(await upgrade.start(), 'run')
  assert.equal(readRecord(recordFile).pending, undefined)
})

test('a run that cannot upgrade itself says how to, once', async () => {
  const { upgrade, logs } = await setup('0.1.0', { unavailable: 'it runs from a checkout' })
  upgrade.announced({ target: '0.2.0', minimum: '0.1.5' })
  upgrade.announced({ target: '0.2.0', minimum: '0.1.5' })
  await upgrade.settle()
  assert.equal(logs.filter((line) => line.includes('cannot upgrade itself because it runs from a checkout')).length, 1)
  assert.match(logs.join('\n'), /below the Minimum Version 0\.1\.5; this Computer takes no new run/)
})

test('versions compare as releases', () => {
  assert.equal(below('0.9.0', '0.10.0'), true)
  assert.equal(below('1.0.0', '0.10.0'), false)
  assert.equal(below('0.10.0', '0.10.0'), false)
  assert.equal(below('0.1.0', ''), false, 'no minimum')
  assert.equal(below('dev', '0.1.0'), true, 'an unreadable version is below every minimum')
})
