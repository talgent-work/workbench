import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { PipeSink } from '../src/execd/pipe.ts'
import type { ExecRequest, StreamOpen } from '../src/execd/service.ts'
import { FILE_CHUNK_BYTES } from '../src/execd/work-files.ts'
import { FILE_WINDOW_BYTES, Tunnel, uuidFromBytes, uuidToBytes, type TunnelService } from '../src/tunnel.ts'

const STREAM = '0f8fad5b-d9cb-469f-a165-70867728950e'

function harness(service: Partial<TunnelService>) {
  const texts: Record<string, unknown>[] = []
  const binaries: Buffer[] = []
  let buffered = 0
  const tunnel = new Tunnel(
    {
      request: service.request ?? (async () => ({ status: 404 })),
      openStream: service.openStream ?? (async () => ({ status: 404 })),
    },
    { text: (message) => texts.push(message), binary: (frame) => binaries.push(frame), buffered: () => buffered },
    () => {},
  )
  return { tunnel, texts, binaries, setBuffered: (value: number) => { buffered = value } }
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 10))

test('stream ids travel as the UUID bytes', () => {
  const bytes = uuidToBytes(STREAM)
  assert.equal(bytes?.length, 16)
  assert.equal(uuidFromBytes(bytes as Buffer), STREAM)
  assert.equal(uuidToBytes('not-a-uuid'), undefined)
})

test('an exec_request is answered by its request id, with the body only when there is one', async () => {
  const seen: ExecRequest[] = []
  const { tunnel, texts } = harness({
    request: async (request) => {
      seen.push(request)
      return request.path === '/ping' ? { status: 200 } : { status: 201, body: { session_id: 'p-1' } }
    },
  })
  tunnel.text({ type: 'exec_request', request_id: 'r-1', work_id: 'w-1', method: 'GET', path: '/ping', token: 't' })
  tunnel.text({ type: 'exec_request', request_id: 'r-2', work_id: 'w-1', method: 'POST', path: '/pty', token: 't', body: { cwd: '/workspace', command: 'x' } })
  tunnel.text({ type: 'something_new' })
  await tick()
  assert.deepEqual(seen[1], { workId: 'w-1', method: 'POST', path: '/pty', token: 't', body: { cwd: '/workspace', command: 'x' } })
  assert.deepEqual(texts, [
    { type: 'exec_response', request_id: 'r-1', status: 200 },
    { type: 'exec_response', request_id: 'r-2', status: 201, body: { session_id: 'p-1' } },
  ])
})

test('a failing request answers 500', async () => {
  const { tunnel, texts } = harness({ request: async () => { throw new Error('boom') } })
  tunnel.text({ type: 'exec_request', request_id: 'r-1', work_id: 'w', method: 'GET', path: '/ping', token: '' })
  await tick()
  assert.deepEqual(texts, [{ type: 'exec_response', request_id: 'r-1', status: 500 }])
})

test('a refused stream_open answers stream_close with the status', async () => {
  const opens: StreamOpen[] = []
  const { tunnel, texts } = harness({ openStream: async (open) => { opens.push(open); return { status: 409 } } })
  tunnel.text({ type: 'stream_open', stream_id: STREAM, work_id: 'w-1', process_id: 'p-1', since: 12, token: 't' })
  await tick()
  assert.deepEqual(opens, [{ workId: 'w-1', processId: 'p-1', since: 12, token: 't' }])
  assert.deepEqual(texts, [{ type: 'stream_close', stream_id: STREAM, status: 409 }])
})

test('stream frames are routed both ways and either side ends the stream', async () => {
  let sink: PipeSink | undefined
  const inbound: (string | Buffer)[] = []
  const { tunnel, texts, binaries, setBuffered } = harness({
    openStream: async (_open, given) => {
      sink = given
      return { text: (text) => inbound.push(text), binary: (frame) => inbound.push(frame) }
    },
  })
  tunnel.text({ type: 'stream_open', stream_id: STREAM, work_id: 'w', process_id: 'p', since: 0, token: 't' })
  // Frames that arrive before the pipe opened are delivered in order.
  tunnel.binary(Buffer.concat([uuidToBytes(STREAM) as Buffer, Buffer.from([0, 0x61])]))
  tunnel.text({ type: 'stream_text', stream_id: STREAM, text: '{"type":"signal","signal":"SIGINT"}' })
  await tick()
  assert.deepEqual(inbound, [Buffer.from([0, 0x61]), '{"type":"signal","signal":"SIGINT"}'])
  sink?.text('{"type":"connected"}\n')
  sink?.binary(Buffer.from([1, 0x62]))
  assert.deepEqual(texts, [{ type: 'stream_text', stream_id: STREAM, text: '{"type":"connected"}\n' }])
  assert.deepEqual(binaries, [Buffer.concat([uuidToBytes(STREAM) as Buffer, Buffer.from([1, 0x62])])])

  setBuffered(10 * 1024 * 1024)
  let drained = false
  void sink?.writable().then(() => { drained = true })
  await tick()
  assert.equal(drained, false)
  setBuffered(0)
  await tick()
  assert.equal(drained, true)

  tunnel.text({ type: 'stream_close', stream_id: STREAM, status: 0 })
  assert.equal(sink?.closed.aborted, true)
  sink?.end()
  assert.equal(texts.length, 1)
})

test('the pipe ending sends stream_close 0, and a closed connection ends every stream', async () => {
  const sinks: PipeSink[] = []
  const { tunnel, texts } = harness({
    openStream: async (_open, given) => {
      sinks.push(given)
      return { text: () => {}, binary: () => {} }
    },
  })
  const other = '1f8fad5b-d9cb-469f-a165-70867728950e'
  tunnel.text({ type: 'stream_open', stream_id: STREAM, work_id: 'w', process_id: 'p', since: 0, token: 't' })
  tunnel.text({ type: 'stream_open', stream_id: other, work_id: 'w', process_id: 'q', since: 0, token: 't' })
  await tick()
  sinks[0].end()
  assert.deepEqual(texts, [{ type: 'stream_close', stream_id: STREAM, status: 0 }])
  tunnel.close()
  assert.equal(sinks[1].closed.aborted, true)
})

test('a stream whose pipe has not opened yet ends once it holds too many early frames', async () => {
  let open: (() => void) | undefined
  let sink: PipeSink | undefined
  const inbound: Buffer[] = []
  const { tunnel, texts } = harness({
    openStream: (_open, given) => {
      sink = given
      return new Promise((resolve) => { open = () => resolve({ text: () => {}, binary: (frame) => inbound.push(frame) }) })
    },
  })
  tunnel.text({ type: 'stream_open', stream_id: STREAM, work_id: 'w', process_id: 'p', since: 0, token: 't' })
  const frame = Buffer.concat([uuidToBytes(STREAM) as Buffer, Buffer.from([0, 0x61])])
  for (let i = 0; i < 256; i++) tunnel.binary(frame)
  assert.deepEqual(texts, [], 'the backlog is held while it fits')
  tunnel.binary(frame)
  assert.deepEqual(texts, [{ type: 'stream_close', stream_id: STREAM, status: 0 }])
  assert.equal(sink?.closed.aborted, true)
  tunnel.binary(frame)
  open?.()
  await tick()
  assert.equal(texts.length, 1, 'the ended stream sends nothing more')
  assert.deepEqual(inbound, [], 'nor delivers what it held')
})

test('a file stream_open reaches the service as a file read, and its end may carry a status', async () => {
  const opens: StreamOpen[] = []
  const sinks: PipeSink[] = []
  const { tunnel, texts, binaries } = harness({
    openStream: async (open, given) => {
      opens.push(open)
      sinks.push(given)
      return { text: () => {}, binary: () => {} }
    },
  })
  const other = '1f8fad5b-d9cb-469f-a165-70867728950e'
  tunnel.text({ type: 'stream_open', stream_id: STREAM, work_id: 'w-1', token: 't', kind: 'file', path: 'docs/a.md' })
  tunnel.text({ type: 'stream_open', stream_id: other, work_id: 'w-1', token: 't', kind: 'file' })
  await tick()
  assert.deepEqual(opens, [
    { kind: 'file', workId: 'w-1', path: 'docs/a.md', token: 't' },
    { kind: 'file', workId: 'w-1', path: '', token: 't' },
  ])
  sinks[0].binary(Buffer.from('raw bytes'))
  sinks[0].end()
  sinks[1].end(500)
  assert.deepEqual(binaries, [Buffer.concat([uuidToBytes(STREAM) as Buffer, Buffer.from('raw bytes')])])
  assert.deepEqual(texts, [
    { type: 'stream_close', stream_id: STREAM, status: 0 },
    { type: 'stream_close', stream_id: other, status: 500 },
  ])
})

test('a file read sends at most its window beyond the credit the Gateway returned', async () => {
  let sink: PipeSink | undefined
  const { tunnel, binaries } = harness({
    openStream: async (_open, given) => {
      sink = given
      return { text: () => {}, binary: () => {} }
    },
  })
  tunnel.text({ type: 'stream_open', stream_id: STREAM, work_id: 'w', token: 't', kind: 'file', path: 'a' })
  await tick()
  const file = sink as PipeSink
  const piece = Buffer.alloc(FILE_CHUNK_BYTES)
  let waiting = false
  for (;;) {
    let resolved = false
    const ready = file.writable().then(() => { resolved = true })
    await tick()
    if (!resolved) {
      waiting = true
      tunnel.text({ type: 'stream_credit', stream_id: STREAM, bytes: 'many' })
      tunnel.text({ type: 'stream_credit', stream_id: STREAM, bytes: -1 })
      await tick()
      assert.equal(resolved, false, 'only a positive byte count is credit')
      tunnel.text({ type: 'stream_credit', stream_id: STREAM, bytes: FILE_CHUNK_BYTES })
      await ready
      file.binary(piece)
      break
    }
    file.binary(piece)
  }
  assert.ok(waiting)
  assert.equal(binaries.length, FILE_WINDOW_BYTES / FILE_CHUNK_BYTES + 1, 'a window, then a piece per credit')

  // A stream that ends releases a read waiting for credit.
  const ready = file.writable()
  tunnel.text({ type: 'stream_close', stream_id: STREAM })
  await ready
  assert.equal(file.closed.aborted, true)
})

test('a pipe sends without credit', async () => {
  let sink: PipeSink | undefined
  const { tunnel } = harness({ openStream: async (_open, given) => { sink = given; return { text: () => {}, binary: () => {} } } })
  tunnel.text({ type: 'stream_open', stream_id: STREAM, work_id: 'w', process_id: 'p', since: 0, token: 't' })
  await tick()
  const pipe = sink as PipeSink
  for (let i = 0; i <= FILE_WINDOW_BYTES / FILE_CHUNK_BYTES; i++) pipe.binary(Buffer.alloc(FILE_CHUNK_BYTES))
  await pipe.writable()
})
