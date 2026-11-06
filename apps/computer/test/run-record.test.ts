import assert from 'node:assert/strict'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { forgetRun, readRun, recordRun } from '../src/run-record.ts'

test('a run records itself and its starter, and forgets only its own record', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'run-record-'))
  try {
    const file = join(dir, 'run.json')
    await recordRun(file)
    assert.deepEqual(await readRun(file), { pid: process.pid, ppid: process.ppid })
    await writeFile(file, JSON.stringify({ pid: process.pid + 1, ppid: 1 }))
    await forgetRun(file)
    assert.deepEqual(await readRun(file), { pid: process.pid + 1, ppid: 1 }, "another run's record stays")
    await writeFile(file, 'not json')
    assert.equal(await readRun(file), undefined)
  } finally {
    await rm(dir, { recursive: true, force: true })
  }
})
