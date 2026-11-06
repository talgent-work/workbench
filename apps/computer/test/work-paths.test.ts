import assert from 'node:assert/strict'
import { posix, win32 } from 'node:path'
import { test } from 'node:test'
import { mapCwd, within, workspaceRelative } from '../src/execd/work-paths.ts'

const root = '/home/u/.local/share/talgent-computer/works/w-1'

test('a cwd under /workspace maps into the Work root', () => {
  assert.equal(mapCwd('/workspace', root, posix), root)
  assert.equal(mapCwd('/workspace/', root, posix), root)
  assert.equal(mapCwd('/workspace/repo/src', root, posix), `${root}/repo/src`)
  assert.equal(mapCwd('/workspace/../etc', root, posix), '/etc')
  assert.equal(mapCwd('/workspace/a/../../..', root, posix), '/')
  assert.equal(mapCwd('/workspaces/x', root, posix), '/workspaces/x')
  assert.equal(mapCwd('/tmp', root, posix), '/tmp')
  assert.equal(mapCwd('', root, posix), root)
  assert.equal(mapCwd('repo', root, posix), `${root}/repo`)
  assert.equal(mapCwd('../outside', root, posix), root)
})

test('on Windows the platform still names /workspace POSIX-style; machine paths are Windows paths', () => {
  const winRoot = 'C:\\Users\\u\\AppData\\Local\\talgent-computer\\works\\w-1'
  assert.equal(mapCwd('/workspace', winRoot, win32), winRoot)
  assert.equal(mapCwd('/workspace/repo/src', winRoot, win32), `${winRoot}\\repo\\src`)
  assert.equal(mapCwd('repo/src', winRoot, win32), `${winRoot}\\repo\\src`)
  assert.equal(mapCwd('repo\\..\\..\\..', winRoot, win32), winRoot, 'a relative cwd never leaves the Work root')
  assert.equal(mapCwd('D:\\elsewhere', winRoot, win32), 'D:\\elsewhere')
  assert.equal(mapCwd('C:/Users/u/project', winRoot, win32), 'C:\\Users\\u\\project')
  assert.equal(mapCwd('', winRoot, win32), winRoot)
})

test('within holds below a directory only, whatever the separator or drive', () => {
  assert.equal(within('/a/b', '/a/b', posix), true)
  assert.equal(within('/a/b', '/a/b/c', posix), true)
  assert.equal(within('/a/b', '/a/b/..c', posix), true, 'a name starting with two dots is inside')
  assert.equal(within('/a/b', '/a', posix), false)
  assert.equal(within('/a/b', '/a/bc', posix), false)
  assert.equal(within('C:\\w\\r', 'C:\\w\\r\\x', win32), true)
  assert.equal(within('C:\\w\\r', 'c:\\W\\R\\x', win32), true, 'Windows paths compare case-insensitively')
  assert.equal(within('C:\\w\\r', 'C:\\w\\r\\..\\s', win32), false)
  assert.equal(within('C:\\w\\r', 'D:\\w\\r\\x', win32), false, 'another drive')
  assert.equal(within('C:\\w\\r', 'C:/w/r/x', win32), true)
})

test('only POSIX /workspace paths name the Work root', () => {
  assert.equal(workspaceRelative('/workspace/a/b'), 'a/b')
  assert.equal(workspaceRelative('\\workspace\\a'), undefined)
  assert.equal(workspaceRelative('C:\\workspace\\a'), undefined)
  assert.equal(workspaceRelative('workspace/a'), undefined)
})
