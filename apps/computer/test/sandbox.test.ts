import { test } from 'node:test'
import assert from 'node:assert/strict'
import { claudeCodeSandboxAvailable, sandboxHeader } from '../src/sandbox.ts'

const only = (...files: string[]) => (file: string) => files.includes(file)

test('Linux provides the sandbox only with bubblewrap and socat on PATH', () => {
  const path = '/usr/local/bin:/usr/bin'
  assert.equal(claudeCodeSandboxAvailable({ platform: 'linux', path, executable: only('/usr/bin/bwrap', '/usr/local/bin/socat') }), true)
  assert.equal(claudeCodeSandboxAvailable({ platform: 'linux', path, executable: only('/usr/bin/bwrap') }), false)
  assert.equal(claudeCodeSandboxAvailable({ platform: 'linux', path: undefined, executable: () => true }), false)
})

test('macOS provides the sandbox through sandbox-exec', () => {
  assert.equal(claudeCodeSandboxAvailable({ platform: 'darwin', path: '', executable: only('/usr/bin/sandbox-exec') }), true)
  assert.equal(claudeCodeSandboxAvailable({ platform: 'darwin', path: '', executable: () => false }), false)
})

test('native Windows never provides the sandbox', () => {
  assert.equal(sandboxHeader({ platform: 'win32', path: 'C:\\Windows', executable: () => true }), 'unavailable')
  assert.equal(sandboxHeader({ platform: 'darwin', path: '', executable: () => true }), 'available')
})
