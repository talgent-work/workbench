import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { test } from 'node:test'
import { shimAlive } from '../src/execd/posix-host.ts'
import { posixOnly } from './support/platform.ts'

// A stand-in shim: its last two arguments are the state directory and run.
async function standIn(dir: string) {
  const child = spawn(process.execPath, ['-e', 'setTimeout(() => {}, 30000)', dir, '1'], { stdio: 'ignore' })
  await once(child, 'spawn')
  return child
}

for (const [platform, useProcfs] of [['/proc', true], ['ps, as on macOS', false]] as const) {
  test(`a running shim is told from a reused pid through ${platform}`, { skip: posixOnly('Windows tells a shim alive by its pipe') }, async (t) => {
    if (useProcfs && process.platform !== 'linux') return t.skip('no /proc')
    const dir = '/tmp/talgent computer/state/w/p'
    const child = await standIn(dir)
    try {
      assert.equal(await shimAlive(child.pid!, dir, useProcfs), true)
      assert.equal(await shimAlive(child.pid!, '/tmp/talgent computer/state/w', useProcfs), false, 'another state directory')
      const deep = `/tmp/${'d'.repeat(600)}/state/w/p`
      const long = await standIn(deep)
      try {
        assert.equal(await shimAlive(long.pid!, deep, useProcfs), true, 'a long command line is read in full')
      } finally {
        long.kill('SIGKILL')
        await once(long, 'exit')
      }
    } finally {
      child.kill('SIGKILL')
      await once(child, 'exit')
    }
    assert.equal(await shimAlive(child.pid!, dir, useProcfs), false, 'an ended shim')
  })
}
