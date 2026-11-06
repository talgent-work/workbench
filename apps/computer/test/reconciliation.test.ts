import assert from 'node:assert/strict'
import { access } from 'node:fs/promises'
import { join } from 'node:path'
import { after, before, describe, test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { output, Pipe } from './support/execd-client.ts'
import type { Decision, Inventory } from './support/gateway.ts'
import { computerTarget, eventually } from './support/targets.ts'

// Reconciliation on reconnecting, driven through the Gateway protocol against the
// executable: every connection opens a round in which nothing new is
// reserved or started until the platform's decision for that round ends the
// processes it names and removes the Works it names.

async function exists(path: string): Promise<boolean> {
  try {
    await access(path)
    return true
  } catch {
    return false
  }
}

describe('reconciliation on reconnecting', () => {
  let target: Awaited<ReturnType<typeof computerTarget>>
  // What the stand-in platform decides on each connection; undefined leaves
  // the round pending for the test to decide.
  let decide: (inventory: Inventory) => Omit<Decision, 'round'> | undefined = () => ({})
  before(async () => { target = await computerTarget({ reconcile: (inventory) => decide(inventory) }) })
  after(() => target.close())

  test('nothing starts until the round is decided; named processes end and never run again', async () => {
    const { client, gateway, cwd } = target
    const leftover = await client.create(cwd, 'echo started; while :; do sleep 1; done')
    const pipe = await client.attach(leftover, 0)
    assert.ok(pipe instanceof Pipe)
    await pipe.until((_frame, seen) => output(seen, 'stdout') === 'started\n')
    pipe.detach()
    // Reserved before the machine went away and never run, as a start the
    // platform withdrew while it was away.
    const reserved = await client.create(cwd, 'echo ran > ran.txt')

    decide = () => undefined
    await target.drop?.()
    const reservation = await client.call('POST', '/pty', { cwd, command: 'true' })
    assert.equal(reservation.status, 409)
    assert.equal((reservation.body as { code: string }).code, 'RECONCILIATION_PENDING')
    // An attach that would start the reserved process waits for the round.
    const waiting = await client.attach(reserved, 0)
    assert.ok(waiting instanceof Pipe)
    await waiting.until((frame) => frame.type === 'connected')
    await delay(300)
    assert.equal((await client.status(reserved)).running, false)
    assert.equal(await exists(join(gateway.workRoot, 'conformance', 'ran.txt')), false)
    // A running process may still be attached to.
    assert.equal((await client.status(leftover)).running, true)

    const inventory = await gateway.inventory()
    assert.equal(inventory.reconciled, false)
    const work = inventory.works.find((entry) => entry.work_id === 'work-1')
    assert.deepEqual(new Set(work?.processes), new Set([leftover, reserved]))
    assert.deepEqual(work?.exited, [], 'neither process has exited')
    const ping = await client.call('GET', '/ping')
    assert.deepEqual(ping.body, { reconciled: false }, 'the ping reports the undecided round')
    const applied = await gateway.reconcile({
      round: inventory.round,
      end_processes: [{ work_id: 'work-1', process_id: leftover }, { work_id: 'work-1', process_id: reserved }],
    })
    assert.equal(applied.status, 204)
    await waiting.ended()
    assert.equal((await client.status(leftover)).status, 404)
    assert.equal((await client.status(reserved)).status, 404)
    // An attach to an ended process is refused instead of running it again.
    const again = await client.attach(reserved, 0)
    assert.deepEqual(again, { refused: 404 })
    assert.equal(await exists(join(gateway.workRoot, 'conformance', 'ran.txt')), false)
    assert.equal((await gateway.inventory()).reconciled, true)
    assert.deepEqual((await client.call('GET', '/ping')).body, { reconciled: true })
    // The round decided, new runs start.
    const fresh = await client.create(cwd, 'echo fresh; while :; do sleep 1; done')
    const run = await client.attach(fresh, 0)
    assert.ok(run instanceof Pipe)
    await run.until((_frame, seen) => output(seen, 'stdout') === 'fresh\n')
    run.detach()
    // A repeated or late decision for the decided round changes nothing,
    // even naming a process that started since.
    const repeated = await gateway.reconcile({ round: inventory.round, end_processes: [{ work_id: 'work-1', process_id: fresh }], remove_works: ['work-1'] })
    assert.equal(repeated.status, 204)
    assert.equal((await client.status(fresh)).running, true)
    assert.equal(await exists(gateway.workRoot), true)
    assert.equal((await client.call('DELETE', `/pty/${fresh}`)).status, 200)
    decide = () => ({})
  })

  test('a decision for an earlier round is refused and the new round stays pending', async () => {
    const { gateway } = target
    const earlier = (await gateway.inventory()).round
    decide = () => undefined
    await target.drop?.()
    const refused = await gateway.reconcile({ round: earlier })
    assert.equal(refused.status, 409)
    assert.equal((refused.body as { code: string }).code, 'ROUND_SUPERSEDED')
    const current = await gateway.inventory()
    assert.notEqual(current.round, earlier)
    assert.equal(current.reconciled, false)
    assert.equal((await gateway.reconcile({ round: current.round })).status, 204)
    decide = () => ({})
  })

  test('a closed Work loses its processes, its files and everything kept for it', async () => {
    const { client, gateway, cwd, dataDir } = target
    // Its inputs are read-only copies, as the inputs sync leaves them.
    const running = await client.create(cwd, 'mkdir -p ../inputs && echo x > ../inputs/brief.txt && chmod 0444 ../inputs/brief.txt && chmod 0555 ../inputs; echo up; while :; do sleep 1; done')
    const pipe = await client.attach(running, 0)
    assert.ok(pipe instanceof Pipe)
    await pipe.until((_frame, seen) => output(seen, 'stdout') === 'up\n')
    pipe.detach()
    const root = gateway.workRoot
    decide = () => undefined
    await target.drop?.()
    const inventory = await gateway.inventory()
    assert.equal((await gateway.reconcile({ round: inventory.round, remove_works: ['work-1'] })).status, 204)
    assert.equal(await exists(root), false)
    assert.equal(await exists(join(dataDir, 'state', 'works', 'work-1.json')), false)
    assert.equal(await exists(join(dataDir, 'state', 'processes', 'work-1')), false)
    await eventually(async () => (await client.status(running)).status === 404)
    decide = () => ({})
  })
})

// Each drop backs the machine's reconnection off further, so these run on a
// machine of their own.
describe('reconciliation as the platform announces it', () => {
  let target: Awaited<ReturnType<typeof computerTarget>>
  let decide: (inventory: Inventory) => Omit<Decision, 'round'> | undefined = () => ({})
  before(async () => { target = await computerTarget({ reconcile: (inventory) => decide(inventory) }) })
  after(() => target.close())

  test('an exited process is reported, so the platform can keep a stopped Harness from running again', async () => {
    const { client, gateway, cwd } = target
    const done = await client.create(cwd, 'echo once')
    const pipe = await client.attach(done, 0)
    assert.ok(pipe instanceof Pipe)
    await pipe.until((frame) => frame.type === 'exit')
    decide = () => undefined
    await target.drop?.()
    const inventory = await gateway.inventory()
    const work = inventory.works.find((entry) => entry.work_id === 'work-1')
    assert.ok(work?.processes.includes(done))
    assert.deepEqual(work?.exited, [done])
    assert.equal((await gateway.reconcile({ round: inventory.round, end_processes: [{ work_id: 'work-1', process_id: done }] })).status, 204)
    assert.deepEqual(await client.attach(done, 0), { refused: 404 })
    decide = () => ({})
  })

  test('a platform that does not announce reconciliation holds back no start', async () => {
    const { client, gateway, cwd } = target
    decide = () => undefined
    await target.drop?.()
    assert.equal((await client.call('POST', '/pty', { cwd, command: 'true' })).status, 409)
    gateway.reconciles = false
    await target.drop?.()
    assert.deepEqual((await client.call('GET', '/ping')).body, { reconciled: true })
    const free = await client.create(cwd, 'echo free')
    const run = await client.attach(free, 0)
    assert.ok(run instanceof Pipe)
    await run.until((frame) => frame.type === 'exit')
    gateway.reconciles = true
    decide = () => ({})
  })
})
