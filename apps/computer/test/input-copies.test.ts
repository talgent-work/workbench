import assert from 'node:assert/strict'
import { chmod, mkdir, readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { after, before, describe, test } from 'node:test'
import { computerTarget, exists, SETUP_HOOK } from './support/targets.ts'
import { portableProcessRules } from './support/platform.ts'

// Earlier releases kept copies of a Work's inputs in its inputs/ directory,
// read-only, with a download record per Work. The first start after the
// upgrade removes those copies and records once; from then on inputs/
// belongs to the Work's Agent.

describe('input copies of earlier releases', { skip: portableProcessRules }, () => {
  let target: Awaited<ReturnType<typeof computerTarget>>
  before(async () => { target = await computerTarget() }, SETUP_HOOK)
  after(() => target?.close(), SETUP_HOOK)

  const inputs = () => join(target.gateway.workRoot, 'inputs')
  const records = () => join(target.dataDir, 'state', 'inputs')
  const record = (workId: string) => writeFile(join(records(), `${workId}.json`), JSON.stringify({ 'brief.md': { size: 9, updatedAt: '2026-01-01T00:00:00.000Z' } }))

  test('the first start removes them once and keeps what others placed; later starts leave inputs/ alone', async () => {
    await target.restart?.(async () => {
      // As an earlier release left them: read-only copies, one it had not
      // recorded yet, and an unfinished download, in a read-only directory.
      await mkdir(inputs(), { recursive: true })
      for (const copy of ['brief.md', 'unrecorded.md']) {
        await writeFile(join(inputs(), copy), 'outdated')
        await chmod(join(inputs(), copy), 0o444)
      }
      await writeFile(join(inputs(), '.0a1b2c3d4e5f.talgent-download'), 'partial')
      await writeFile(join(inputs(), 'notes.md'), 'placed by the person')
      await chmod(inputs(), 0o555)
      await mkdir(records(), { recursive: true })
      await record('work-1')
      // A Work removed from the machine leaves only its record.
      await record('gone-work')
    })
    assert.deepEqual(await readdir(inputs()), ['notes.md'])
    assert.equal(await exists(records()), false)

    // The Work's Agent downloads into inputs/ from now on, under the names
    // the copies had; a start that finds a record again still spares them.
    await writeFile(join(inputs(), 'brief.md'), 'fetched by the Agent')
    await target.restart?.()
    await target.restart?.(async () => {
      await mkdir(records(), { recursive: true })
      await record('work-1')
    })
    assert.deepEqual((await readdir(inputs())).sort(), ['brief.md', 'notes.md'])
    assert.equal(await readFile(join(inputs(), 'brief.md'), 'utf8'), 'fetched by the Agent')
  })

  test('the Work inputs route is gone', async () => {
    assert.equal((await target.client.call('PUT', '/inputs', { files: [] })).status, 404)
  })
})
