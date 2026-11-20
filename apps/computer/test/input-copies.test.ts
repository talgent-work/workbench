import assert from 'node:assert/strict'
import { chmod, mkdir, readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { after, before, describe, test } from 'node:test'
import { computerTarget, exists, SETUP_HOOK } from './support/targets.ts'

// Earlier releases kept platform-made copies of a Work's inputs in its
// inputs/ directory, read-only, with a download record per Work. The first
// start after the upgrade removes those copies and records once; from then
// on inputs/ belongs to the Work's Agent.

describe('platform-made input copies after the upgrade', () => {
  let target: Awaited<ReturnType<typeof computerTarget>>
  before(async () => { target = await computerTarget() }, SETUP_HOOK)
  after(() => target?.close(), SETUP_HOOK)

  const inputs = () => join(target.gateway.workRoot, 'inputs')
  const records = () => join(target.dataDir, 'state', 'inputs')

  test('the first start removes the copies and their records and keeps what others placed', async () => {
    await target.restart?.(async () => {
      // As the downloader left them: read-only copies in a read-only directory.
      await mkdir(inputs(), { recursive: true })
      await writeFile(join(inputs(), 'brief.md'), 'old brief')
      await writeFile(join(inputs(), '.0a1b2c3d4e5f.talgent-download'), 'partial')
      await writeFile(join(inputs(), 'notes.md'), 'placed by the person')
      await chmod(join(inputs(), 'brief.md'), 0o444)
      await chmod(inputs(), 0o555)
      await mkdir(records(), { recursive: true })
      await writeFile(join(records(), 'work-1.json'), JSON.stringify({ 'brief.md': { size: 9, updatedAt: '2026-01-01T00:00:00.000Z' } }))
      // A Work removed from the machine leaves only its record.
      await writeFile(join(records(), 'gone-work.json'), JSON.stringify({ 'plan.md': { size: 1, updatedAt: '2026-01-01T00:00:00.000Z' } }))
    })
    assert.deepEqual(await readdir(inputs()), ['notes.md'])
    assert.equal(await exists(records()), false)
    // The Work's Agent downloads into inputs/ from now on.
    await writeFile(join(inputs(), 'fetched.md'), 'fetched by the Agent')
  })

  test('later starts leave inputs/ untouched', async () => {
    await target.restart?.()
    assert.deepEqual((await readdir(inputs())).sort(), ['fetched.md', 'notes.md'])
    assert.equal(await readFile(join(inputs(), 'fetched.md'), 'utf8'), 'fetched by the Agent')
  })

  test('the Work inputs route is gone', async () => {
    assert.equal((await target.client.call('PUT', '/inputs', { files: [] })).status, 404)
  })
})
