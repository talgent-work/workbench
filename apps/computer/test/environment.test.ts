import assert from 'node:assert/strict'
import { test } from 'node:test'
import { deleteVariable, mergeEnvironment, prependPath } from '../src/execd/environment.ts'

test('on Linux and macOS variable names are case-sensitive', () => {
  const env = mergeEnvironment('linux', { PATH: '/usr/bin', Path: 'kept', A: '1' }, { A: '2' })
  prependPath(env, '/opt/claude/bin', 'linux')
  assert.deepEqual(env, { PATH: '/opt/claude/bin:/usr/bin', Path: 'kept', A: '2' })
  deleteVariable(env, 'path', 'linux')
  assert.equal(env.PATH, '/opt/claude/bin:/usr/bin', 'another name')
})

test('on Windows a later layer replaces a variable whatever its case, and PATH stays one variable', () => {
  const env = mergeEnvironment('win32', { Path: 'C:\\Windows', Anthropic_Api_Key: 'machine', SystemRoot: 'C:\\Windows' }, { PATH: 'C:\\work' })
  assert.deepEqual(env, { Anthropic_Api_Key: 'machine', SystemRoot: 'C:\\Windows', PATH: 'C:\\work' })
  prependPath(env, 'C:\\claude\\bin', 'win32')
  assert.equal(env.PATH, 'C:\\claude\\bin;C:\\work')
  deleteVariable(env, 'ANTHROPIC_API_KEY', 'win32')
  assert.deepEqual(Object.keys(env).sort(), ['PATH', 'SystemRoot'])
  const empty: NodeJS.ProcessEnv = {}
  prependPath(empty, 'C:\\claude\\bin', 'win32')
  assert.deepEqual(empty, { PATH: 'C:\\claude\\bin' })
})
