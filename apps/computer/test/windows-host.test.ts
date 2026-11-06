import assert from 'node:assert/strict'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, win32 } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, describe, test } from 'node:test'
import { openPipe, type PipeInput, type PipeSink } from '../src/execd/pipe.ts'
import { ProcessTable } from '../src/execd/processes.ts'
import { findGitBash, namedPipe } from '../src/execd/windows-host.ts'
import { windowsLikeHost } from './support/windows-like-host.ts'

const SHIM = fileURLToPath(new URL('./support/windows-like-shim.ts', import.meta.url))

test('Git Bash is found as Claude Code finds it, never WSL bash', () => {
  const files = new Set([
    'C:\\Program Files\\Git\\cmd\\git.exe',
    'C:\\Program Files\\Git\\bin\\bash.exe',
    'C:\\Windows\\System32\\bash.exe',
    'D:\\tools\\git\\bin\\bash.exe',
    'E:\\PortableGit\\bin\\git.exe',
    'E:\\PortableGit\\bin\\bash.exe',
  ])
  const exists = (path: string) => files.has(path)
  const path = 'C:\\Windows\\System32;C:\\Program Files\\Git\\cmd'
  assert.equal(findGitBash({ Path: path }, exists), 'C:\\Program Files\\Git\\bin\\bash.exe', 'beside git on PATH, whatever the variable\'s case')
  assert.equal(findGitBash({ PATH: 'E:\\PortableGit\\bin' }, exists), 'E:\\PortableGit\\bin\\bash.exe')
  assert.equal(findGitBash({ PATH: path, CLAUDE_CODE_GIT_BASH_PATH: 'D:\\tools\\git\\bin\\bash.exe' }, exists), 'D:\\tools\\git\\bin\\bash.exe')
  assert.equal(findGitBash({ PATH: 'C:\\Windows\\System32', ProgramFiles: 'C:\\Program Files' }, exists), 'C:\\Program Files\\Git\\bin\\bash.exe')
  assert.equal(findGitBash({ PATH: 'C:\\Windows\\System32' }, exists), undefined)
})

test('each run has its own named pipe', () => {
  const dir = win32.join('C:\\Users\\u\\AppData\\Local\\talgent-computer\\state\\processes', 'w-1', 'p-1')
  assert.match(namedPipe(dir, 1), /^\\\\\.\\pipe\\talgent-computer-[0-9a-f]{40}$/)
  assert.notEqual(namedPipe(dir, 1), namedPipe(dir, 2))
  assert.notEqual(namedPipe(dir, 1), namedPipe(`${dir}x`, 1))
})

// The Windows realization driven end to end, with a Unix socket for the pipe
// and a process group for the tree.
describe('Windows processes', { skip: process.platform === 'win32' ? 'the real realization runs in every suite there' : false }, () => {
  let dir: string
  let table: ProcessTable
  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), 'windows-host-'))
    table = processTable()
  })
  afterEach(() => rm(dir, { recursive: true, force: true }))

  const processTable = () => new ProcessTable({ stateRoot: join(dir, 'state'), shim: [process.execPath, SHIM], host: windowsLikeHost() })

  function attach(id: string, on: ProcessTable = table) {
    const closed = new AbortController()
    const out: Buffer[] = []
    const texts: string[] = []
    let ended = false
    const sink: PipeSink = {
      closed: closed.signal,
      text: (text) => texts.push(text),
      binary: (frame) => out.push(frame),
      end: () => { ended = true },
      writable: async () => {},
    }
    const stdout = () => out.filter((frame) => frame[0] === 1 || frame[0] === 3).map((frame) => frame.subarray(frame[0] === 3 ? 9 : 1).toString()).join('')
    // A replay carries stderr too, and a macOS runner's bash complains on
    // stderr without HOME, which every Work process has.
    const opened = openPipe({ table: on, workId: 'w-1', id, since: 0, env: async () => ({ PATH: process.env.PATH ?? '', HOME: dir }), ready: async () => {}, log: () => {} }, sink)
    return { opened, stdout, texts, closed, ended: () => ended }
  }

  test('stdin arrives through the pipe, output is logged and the exit recorded', async () => {
    const id = await table.reserve('w-1', { cwd: dir, command: 'echo ready; while read l; do echo "L:$l"; [ "$l" = bye ] && exit 3; done' })
    const pipe = attach(id)
    const input = (await pipe.opened) as PipeInput
    await until(() => pipe.stdout() === 'ready\n')
    input.binary(Buffer.from('\0hello\n'))
    await until(() => pipe.stdout() === 'ready\nL:hello\n')
    input.binary(Buffer.from('\0bye\n'))
    await until(() => pipe.ended())
    assert.equal(pipe.stdout(), 'ready\nL:hello\nL:bye\n')
    assert.equal(pipe.texts.at(-1), '{"type":"exit","exit_code":3}\n')
    assert.deepEqual(await table.phase('w-1', id), { kind: 'exited', run: 1, exitCode: 3 })
  })

  test('a signal ends the run\'s tree, which then reads as killed', async () => {
    const id = await table.reserve('w-1', { cwd: dir, command: 'sleep 30 & echo started; wait' })
    const pipe = attach(id)
    const input = (await pipe.opened) as PipeInput
    await until(() => pipe.stdout() === 'started\n')
    const running = await table.phase('w-1', id)
    assert.equal(running?.kind, 'running')
    input.text('{"type":"signal","signal":"SIGINT"}')
    await until(() => pipe.ended())
    assert.equal(pipe.texts.at(-1), '{"type":"exit","exit_code":-1}\n')
    if (running?.kind === 'running') await until(() => !alive(running.started.pid))
  })

  test('a running process is found again by a fresh table over the same state, then deleted', async () => {
    const id = await table.reserve('w-1', { cwd: dir, command: 'echo up; read x; echo "got-$x"' })
    const first = attach(id)
    await first.opened
    await until(() => first.stdout() === 'up\n')
    first.closed.abort()
    await until(() => first.ended())

    const again = processTable()
    const phase = await again.phase('w-1', id)
    assert.equal(phase?.kind, 'running', 'the shim\'s pipe tells it alive')
    const second = attach(id, again)
    const input = (await second.opened) as PipeInput
    input.binary(Buffer.from('\0x\n'))
    await until(() => second.stdout() === 'up\ngot-x\n')
    await until(() => second.ended())

    const id2 = await again.reserve('w-1', { cwd: dir, command: 'echo held; exec sleep 30' })
    const third = attach(id2, again)
    await third.opened
    await until(() => third.stdout() === 'held\n')
    const held = await again.phase('w-1', id2)
    assert.equal(await again.delete('w-1', id2), true)
    if (held?.kind === 'running') await until(() => !alive(held.started.pid))
    assert.equal(await again.phase('w-1', id2), undefined)
  })
})

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

async function until(check: () => boolean, timeoutMs = 10_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (!check()) {
    if (Date.now() > deadline) throw new Error('timed out')
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
}
