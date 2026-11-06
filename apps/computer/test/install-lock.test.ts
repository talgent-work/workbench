import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { InstallLock } from '../src/upgrade/lock.ts'

let dir: string
let file: string
beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'install-lock-'))
  file = join(dir, 'state', 'upgrade.lock')
})
afterEach(() => rm(dir, { recursive: true, force: true }))

async function heldBy(pid: number, at: number): Promise<void> {
  await mkdir(join(dir, 'state'), { recursive: true })
  await writeFile(file, JSON.stringify({ pid, at }))
}

test('a held lock makes the next holder wait, and is released after its task, even a failed one', async () => {
  const lock = new InstallLock({ file, staleAfterMs: 60_000, waitMs: 5_000 })
  const order: string[] = []
  const first = lock.hold(async () => {
    order.push('first starts')
    await new Promise((resolve) => setTimeout(resolve, 300))
    order.push('first ends')
    throw new Error('npm failed')
  })
  const second = lock.hold(async () => { order.push('second') })
  await assert.rejects(first, /npm failed/)
  await second
  assert.deepEqual(order, ['first starts', 'first ends', 'second'])
  assert.equal(existsSync(file), false)
})

test('a lock whose owner is gone, or that is older than any install, is taken over', async () => {
  await heldBy(999_999, Date.now())
  assert.equal(await new InstallLock({ file, staleAfterMs: 60_000, waitMs: 2_000, alive: () => false }).hold(async () => 'ran'), 'ran')
  await heldBy(process.pid, Date.now() - 120_000)
  assert.equal(await new InstallLock({ file, staleAfterMs: 60_000, waitMs: 2_000 }).hold(async () => 'ran'), 'ran', 'a reused pid does not hold it for ever')
})

test('a lock its live owner holds past the wait is reported', async () => {
  await heldBy(process.pid, Date.now())
  await assert.rejects(new InstallLock({ file, staleAfterMs: 60_000, waitMs: 300 }).hold(async () => 'ran'), /another Talgent Computer process is installing/)
  assert.equal(existsSync(file), true, 'the owner keeps its lock')
})
