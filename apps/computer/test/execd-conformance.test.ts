import assert from 'node:assert/strict'
import { writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { after, before, describe, test } from 'node:test'
import { output, Pipe, type Frame } from './support/execd-client.ts'
import { posixOnly } from './support/platform.ts'
import { computerTarget, eventually, execdTarget, exists, type Target } from './support/targets.ts'

// execd's pipe semantics, run against OpenSandbox execd itself when
// EXECD_BINARY names it, and always against Talgent Computer relayed through
// a stand-in Gateway: what the platform's execd client relies on must hold
// for both.

const targets: { name: string; open: () => Promise<Target> }[] = [{ name: 'talgent-computer', open: () => computerTarget() }]
const binary = process.env.EXECD_BINARY
if (binary !== undefined && binary !== '') targets.unshift({ name: 'execd', open: () => execdTarget(binary) })

const isExit = (frame: Frame) => frame.type === 'exit'

for (const { name, open } of targets) {
  describe(`execd pipe: ${name}`, () => {
    let target: Target
    const created: string[] = []
    before(async () => { target = await open() })
    after(async () => {
      for (const id of created) await target.client.call('DELETE', `/pty/${id}`)
      await target.close()
    })
    const create = async (command: string) => {
      const id = await target.client.create(target.cwd, command)
      created.push(id)
      return id
    }
    const attach = async (id: string, since: number) => {
      const pipe = await target.client.attach(id, since)
      assert.ok(pipe instanceof Pipe, `attach refused: ${JSON.stringify(pipe)}`)
      return pipe
    }

    test('a reservation starts on attach; output, stdin, stderr, signal and exit', { skip: posixOnly('a signal ends the process tree on Windows; no trap sees it') }, async () => {
      const id = await create(`trap 'echo got-int; exit 5' INT; echo ready; while read l; do echo "L:$l"; echo "E:$l" >&2; done`)
      assert.deepEqual(await target.client.status(id), { status: 200, running: false, offset: 0 })
      const pipe = await attach(id, 0)
      const first = await pipe.next()
      assert.deepEqual(first, { type: 'connected', session_id: id, mode: 'pipe', role: 'holder' })
      await pipe.until((_frame, seen) => output(seen, 'stdout') === 'ready\n')
      assert.equal((await target.client.status(id)).running, true)
      pipe.stdin('hello\n')
      // stdout and stderr are separate pipes: either may arrive first.
      const seen = await pipe.until((_frame, frames) => output(frames, 'stderr') === 'E:hello\n' && output(frames, 'stdout') === 'L:hello\n')
      assert.equal(output(seen, 'stdout'), 'L:hello\n')
      pipe.signal('SIGINT')
      const rest = await pipe.until(isExit)
      assert.equal(output(rest, 'stdout'), 'got-int\n')
      assert.deepEqual(rest.at(-1), { type: 'exit', exit_code: 5 })
      await pipe.ended()
      const total = 'ready\nL:hello\nE:hello\ngot-int\n'.length
      assert.deepEqual(await target.client.status(id), { status: 200, running: false, offset: total })
    })

    test('a signal reaches the whole process group; a killed shell exits -1', async () => {
      const id = await create('sleep 30 & echo started; wait')
      const pipe = await attach(id, 0)
      await pipe.until((_frame, seen) => output(seen, 'stdout') === 'started\n')
      pipe.signal('SIGKILL')
      const rest = await pipe.until(isExit, 5000)
      assert.deepEqual(rest.at(-1), { type: 'exit', exit_code: -1 })
    })

    test('reattaching from an offset replays exactly the remaining bytes, then goes live', async () => {
      const id = await create('echo first; read x; echo "second-$x"')
      const one = await attach(id, 0)
      await one.until((_frame, seen) => output(seen, 'stdout') === 'first\n')
      one.detach()
      const two = await target.client.attachWhenFree(id, 2)
      const replayed = await two.until((frame) => frame.type === 'connected')
      assert.deepEqual(replayed, [
        { type: 'replay', offset: 2, data: 'rst\n' },
        { type: 'connected', session_id: id, mode: 'pipe', role: 'holder' },
      ])
      two.stdin('z\n')
      const rest = await two.until(isExit)
      assert.equal(output(rest, 'stdout'), 'second-z\n')
      assert.deepEqual(rest.at(-1), { type: 'exit', exit_code: 0 })
    })

    test('a since beyond the output replays nothing', async () => {
      const id = await create('echo out; read x')
      const one = await attach(id, 0)
      await one.until((_frame, seen) => output(seen, 'stdout') === 'out\n')
      one.detach()
      const two = await target.client.attachWhenFree(id, 100)
      assert.deepEqual(await two.next(), { type: 'connected', session_id: id, mode: 'pipe', role: 'holder' })
      two.signal('SIGKILL')
      await two.until(isExit)
    })

    // execd answers an attach to an exited process by replaying its output
    // and running the command again; offsets continue across runs.
    test('attaching after exit replays the output, then runs the command again', async () => {
      const id = await create('echo out; exit 3')
      const one = await attach(id, 0)
      assert.deepEqual((await one.until(isExit)).at(-1), { type: 'exit', exit_code: 3 })
      await one.ended()
      assert.deepEqual(await target.client.status(id), { status: 200, running: false, offset: 4 })
      const two = await attach(id, 0)
      const frames = await two.until(isExit)
      assert.deepEqual(frames, [
        { type: 'replay', offset: 0, data: 'out\n' },
        { type: 'connected', session_id: id, mode: 'pipe', role: 'holder' },
        { type: 'stdout', data: 'out\n' },
        { type: 'exit', exit_code: 3 },
      ])
      assert.deepEqual(await target.client.status(id), { status: 200, running: false, offset: 8 })
    })

    test('a second attach while one is open answers 409', async () => {
      const id = await create('read x')
      const one = await attach(id, 0)
      await one.next()
      assert.deepEqual(await target.client.attach(id, 0), { refused: 409 })
      one.signal('SIGKILL')
      await one.until(isExit)
    })

    test('an unknown frame type answers an error frame', async () => {
      const id = await create('read x')
      const pipe = await attach(id, 0)
      await pipe.next()
      pipe.sendText(JSON.stringify({ type: 'bogus' }))
      assert.deepEqual(await pipe.next(), { type: 'error', error: 'unknown frame type "bogus"', code: 'INVALID_FRAME' })
      pipe.signal('SIGKILL')
      await pipe.until(isExit)
    })

    test('delete kills the process group and is not found afterwards', { skip: posixOnly('Git Bash\'s $$ is not a Windows pid') }, async () => {
      const id = await create('echo $$; exec sleep 30')
      const pipe = await attach(id, 0)
      const seen = await pipe.until((frame) => frame.type === 'stdout')
      const pid = Number(output(seen, 'stdout').trim())
      assert.equal((await target.client.call('DELETE', `/pty/${id}`)).status, 200)
      await eventually(async () => {
        try {
          process.kill(pid, 0)
          return false
        } catch {
          return true
        }
      }, 5000)
      assert.equal((await target.client.call('DELETE', `/pty/${id}`)).status, 404)
      assert.equal((await target.client.status(id)).status, 404)
      pipe.detach()
    })

    test('unknown processes and wrong tokens are refused', async () => {
      const unknown = { code: 'CONTEXT_NOT_FOUND', message: 'pty session nope not found' }
      assert.deepEqual(await target.client.call('GET', '/pty/nope'), { status: 404, body: unknown })
      assert.deepEqual(await target.client.attach('nope', 0), { refused: 404 })
      assert.equal((await target.client.call('POST', '/pty', { cwd: target.cwd, command: 'true' }, 'wrong')).status, 401)
      assert.equal((await target.client.call('GET', '/ping', undefined, 'wrong')).status, 401)
      assert.equal((await target.client.call('GET', '/ping')).status, 200)
    })

    test('a process survives a restart of its server', { skip: name === 'execd' }, async () => {
      // Released only while no server runs, it then writes "during" and
      // marks that it did.
      const id = await create('echo before; while [ ! -e released ]; do sleep 0.02; done; echo during; : > said; read x; echo "after-$x"')
      const one = await attach(id, 0)
      await one.until((_frame, seen) => output(seen, 'stdout') === 'before\n')
      await target.restart?.(async () => {
        await writeFile(join(target.hostCwd, 'released'), '')
        await eventually(async () => exists(join(target.hostCwd, 'said')))
      })
      assert.equal((await target.client.status(id)).running, true)
      // Output written while nobody served the process is kept for replay.
      const two = await target.client.attachWhenFree(id, 7)
      assert.deepEqual(await two.until((frame) => frame.type === 'connected'), [
        { type: 'replay', offset: 7, data: 'during\n' },
        { type: 'connected', session_id: id, mode: 'pipe', role: 'holder' },
      ])
      two.stdin('q\n')
      const rest = await two.until(isExit)
      assert.equal(output(rest, 'stdout'), 'after-q\n')
      assert.deepEqual(rest.at(-1), { type: 'exit', exit_code: 0 })
    })

    test('a network loss shorter than the fence loses no output and keeps the process', { skip: name === 'execd' }, async () => {
      const id = await create('(n=0; while true; do echo "tick-$n"; n=$((n+1)); sleep 0.05; done) & while read l; do echo "L:$l"; done')
      const one = await attach(id, 0)
      one.stdin('before\n')
      const first = await one.until((_frame, seen) => output(seen, 'stdout').includes('L:before\n') && output(seen, 'stdout').includes('tick-2\n'))
      // Live frames carry no offset: the bytes seen from 0 are the offset.
      const seen = output(first, 'stdout')
      const since = Buffer.byteLength(seen)
      await target.drop?.()
      // Output keeps coming while the client is away.
      await eventually(async () => ((await target.client.status(id)).offset ?? 0) > since)
      assert.equal((await target.client.status(id)).running, true, 'no fence within the timeout')
      const two = await target.client.attachWhenFree(id, since)
      const replayed = await two.until((frame) => frame.type === 'connected')
      const replays = replayed.filter((frame) => frame.type === 'replay')
      assert.ok(replays.length > 0, 'output made during the loss is replayed')
      let offset = since
      for (const frame of replays) {
        assert.equal(frame.offset, offset, 'replay offsets are contiguous from since')
        offset += Buffer.byteLength(frame.data)
      }
      two.stdin('after\n')
      const rest = await two.until((_frame, frames) => output(frames, 'stdout').includes('L:after\n'))
      const all = seen + output(replays, 'replay') + output(rest, 'stdout')
      const lines = all.split('\n').filter((line) => line !== '')
      const ticks = lines.filter((line) => line.startsWith('tick-')).map((line) => Number(line.slice(5)))
      assert.deepEqual(ticks, ticks.map((_tick, index) => index), 'every tick once, in order')
      assert.deepEqual(lines.filter((line) => line.startsWith('L:')), ['L:before', 'L:after'])
      two.signal('SIGKILL')
      await two.until(isExit)
    })
  })
}
