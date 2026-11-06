import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const LAUNCHER = fileURLToPath(new URL('../src/launcher.mjs', import.meta.url))

// The talgent-computer command on an older Node, as npm installs it there
// with only a warning about the engine.
test('the command says Node.js 24 is needed and exits non-zero on an older Node', () => {
  const pretend = 'Object.defineProperty(process.versions, "node", { value: "22.11.0" })'
  const result = spawnSync(process.execPath, ['--import', `data:text/javascript,${encodeURIComponent(pretend)}`, LAUNCHER, '--version'], { encoding: 'utf8' })
  assert.equal(result.status, 1)
  assert.equal(result.stdout, '')
  assert.match(result.stderr, /^Talgent Computer needs Node\.js 24 or newer, but this is Node\.js 22\.11\.0\.\nInstall Node\.js 24 from https:\/\/nodejs\.org/)
})
