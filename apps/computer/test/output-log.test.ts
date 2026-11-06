import assert from 'node:assert/strict'
import { appendFileSync, closeSync, openSync, readFileSync, writeSync } from 'node:fs'
import { mkdtemp, open, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { decodeRecords, encodeRecord, LogCursor, LogAppender, MAX_RECORD_DATA, RECORD_HEADER, STDERR, STDOUT } from '../src/execd/output-log.ts'

let dir: string
beforeEach(async () => { dir = await mkdtemp(join(tmpdir(), 'output-log-')) })
afterEach(() => rm(dir, { recursive: true, force: true }))

test('records carry their stream and the offset of their first byte', () => {
  const block = Buffer.concat([encodeRecord(STDOUT, 0, Buffer.from('abc')), encodeRecord(STDERR, 3, Buffer.from('de'))])
  const { records, used } = decodeRecords(Buffer.concat([block, Buffer.from([1, 0, 0])]))
  assert.equal(used, block.length)
  assert.deepEqual(records.map((r) => [r.stream, r.offset, r.data.toString()]), [[STDOUT, 0, 'abc'], [STDERR, 3, 'de']])
  assert.throws(() => encodeRecord(STDOUT, 0, Buffer.alloc(MAX_RECORD_DATA + 1)))
})

test('appending resumes after the last whole record and cuts a torn tail', () => {
  const path = join(dir, 'output.log')
  const first = new LogAppender(path)
  assert.equal(first.offset, 0)
  first.append(STDOUT, Buffer.from('hello'))
  first.close()
  appendFileSync(path, encodeRecord(STDOUT, 5, Buffer.from('world')).subarray(0, RECORD_HEADER + 2))
  const again = new LogAppender(path)
  assert.equal(again.offset, 5)
  assert.equal(readFileSync(path).length, RECORD_HEADER + 5, 'the torn tail is cut')
  again.append(STDERR, Buffer.from('!'))
  again.close()
  const { records, used } = decodeRecords(readFileSync(path))
  assert.equal(used, 2 * RECORD_HEADER + 6)
  assert.deepEqual(records.map((r) => [r.stream, r.offset, r.data.toString()]), [[STDOUT, 0, 'hello'], [STDERR, 5, '!']])
})

test('a cursor reads forward and seeks to a checkpoint at or before an offset', async () => {
  const path = join(dir, 'output.log')
  const fd = openSync(path, 'a+')
  let offset = 0
  const chunk = Buffer.alloc(MAX_RECORD_DATA, 'x')
  for (let i = 0; i < 40; i++) {
    writeSync(fd, encodeRecord(STDOUT, offset, chunk))
    offset += chunk.length
  }
  closeSync(fd)
  const checkpoints: { position: number; offset: number }[] = []
  const cursor = new LogCursor(await open(path, 'r'), checkpoints)
  let read = 0
  for (let records = await cursor.next(); records.length > 0; records = await cursor.next()) {
    for (const record of records) {
      assert.equal(record.offset, read)
      read += record.data.length
    }
  }
  assert.equal(read, offset)
  assert.equal(cursor.offset, offset)
  assert.ok(checkpoints.length > 1)
  cursor.seek(offset - 1)
  const at = cursor.offset
  assert.ok(at <= offset - 1 && at > 0)
  const [first] = await cursor.next()
  assert.equal(first.offset, at)
  cursor.seek(0)
  assert.deepEqual([cursor.position, cursor.offset], [0, 0])
  await cursor.close()
})
