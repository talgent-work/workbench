import assert from 'node:assert/strict'
import { mkdir, mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { CLAUDE_CODE_PACKAGE, ClaudeCodeMissingError, PACKAGE_ROOT, parseClaudeVersion, prepareClaudeCode, removeUnusedCopies, type Run } from '../src/claude-code.ts'

let dir: string
beforeEach(async () => { dir = await mkdtemp(join(tmpdir(), 'claude-code-')) })
afterEach(() => rm(dir, { recursive: true, force: true }))

// Lays out what npm leaves for the dependency under <base>/node_modules: the
// package, whose bin/claude.exe is the stub its install script replaces,
// and the native package of this platform beside it.
async function installAt(base: string, version: string, native = 'linux-x64') {
  const modules = join(base, 'node_modules', '@anthropic-ai')
  const pkg = join(modules, 'claude-code')
  await mkdir(join(pkg, 'bin'), { recursive: true })
  await writeFile(join(pkg, 'package.json'), JSON.stringify({ name: CLAUDE_CODE_PACKAGE, version, bin: { claude: 'bin/claude.exe' } }))
  await writeFile(join(pkg, 'bin', 'claude.exe'), 'stub')
  const executable = join(modules, `claude-code-${native}`, native.startsWith('win32') ? 'claude.exe' : 'claude')
  await mkdir(dirname(executable), { recursive: true })
  await writeFile(join(dirname(executable), 'package.json'), JSON.stringify({ name: `${CLAUDE_CODE_PACKAGE}-${native}`, version }))
  await writeFile(executable, `native ${version}`)
  return { pkg, executable }
}

// Answers --version for a file holding a native executable's stand-in.
function recording(): { run: Run; runs: string[] } {
  const runs: string[] = []
  const run: Run = async (file) => {
    runs.push(file)
    const content = await readFile(file, 'utf8')
    if (!content.startsWith('native ')) throw new Error('claude native binary not installed')
    return `${content.slice('native '.length)} (Claude Code)\n`
  }
  return { run, runs }
}

const linux = { platform: 'linux' as const, arch: 'x64', musl: false }

test('the bundled executable is copied to its version\'s directory, whether or not its install script ran', async () => {
  const copiesDir = join(dir, 'data', 'claude-code')
  const { executable } = await installAt(dir, '2.1.266')
  const packageRoot = join(dir, 'node_modules', '@talgent-work', 'computer')
  await mkdir(packageRoot, { recursive: true })
  const { run, runs } = recording()
  assert.deepEqual(await prepareClaudeCode({ copiesDir, packageRoot, run, ...linux }), { binDir: join(copiesDir, '2.1.266'), version: '2.1.266' })
  assert.equal(await readFile(join(copiesDir, '2.1.266', 'claude'), 'utf8'), await readFile(executable, 'utf8'))
  assert.deepEqual(await readdir(join(copiesDir, '2.1.266')), ['claude'], 'no partial copy is left')
  assert.ok(!runs.includes(executable), 'the package\'s own executable never runs')

  // Next run: the copy that runs is kept.
  await writeFile(executable, 'native 9.9.9')
  assert.equal((await prepareClaudeCode({ copiesDir, packageRoot, run, ...linux })).version, '2.1.266')
})

test('a nested package wins over a hoisted one, and a musl or Windows machine takes its own executable', async () => {
  await installAt(dir, '2.1.1')
  const packageRoot = join(dir, 'node_modules', '@talgent-work', 'computer')
  await installAt(packageRoot, '2.1.266', 'linux-arm64-musl')
  const { run } = recording()
  const copiesDir = join(dir, 'copies')
  assert.equal((await prepareClaudeCode({ copiesDir, packageRoot, run, platform: 'linux', arch: 'arm64', musl: true })).version, '2.1.266')

  const windowsRoot = join(dir, 'win')
  await installAt(windowsRoot, '2.1.266', 'win32-x64')
  const copied = await prepareClaudeCode({ copiesDir: join(dir, 'win-copies'), packageRoot: windowsRoot, run, platform: 'win32', arch: 'x64' })
  assert.deepEqual(await readdir(copied.binDir), ['claude.exe'], 'Windows finds claude.exe as claude')
})

test('a damaged copy is made again', async () => {
  await installAt(dir, '2.1.266')
  const copiesDir = join(dir, 'copies')
  await mkdir(join(copiesDir, '2.1.266'), { recursive: true })
  await writeFile(join(copiesDir, '2.1.266', 'claude'), 'cut short')
  const { run } = recording()
  assert.equal((await prepareClaudeCode({ copiesDir, packageRoot: dir, run, ...linux })).version, '2.1.266')
  assert.equal(await readFile(join(copiesDir, '2.1.266', 'claude'), 'utf8'), 'native 2.1.266')
})

test('a missing package, a missing native executable or one that does not run is reported', async () => {
  const copiesDir = join(dir, 'copies')
  const { run } = recording()
  await assert.rejects(prepareClaudeCode({ copiesDir, packageRoot: dir, run, ...linux }), ClaudeCodeMissingError)
  const { executable } = await installAt(dir, '2.1.266')
  await assert.rejects(prepareClaudeCode({ copiesDir, packageRoot: dir, run, ...linux, arch: 'arm64' }), /claude-code-linux-arm64.*without --omit=optional/)
  await writeFile(executable, 'not for this machine')
  await assert.rejects(prepareClaudeCode({ copiesDir, packageRoot: dir, run, ...linux }), /does not run on this machine/)
  assert.deepEqual(await readdir(join(copiesDir, '2.1.266')), [], 'what does not run is not kept')
})

test('copies no running process uses are removed', async () => {
  const copiesDir = join(dir, 'copies')
  for (const version of ['2.1.1', '2.1.2', '2.1.266', 'bin']) await mkdir(join(copiesDir, version), { recursive: true })
  await removeUnusedCopies(copiesDir, new Set([join(copiesDir, '2.1.266'), join(copiesDir, '2.1.1')]), () => {})
  assert.deepEqual((await readdir(copiesDir)).sort(), ['2.1.1', '2.1.266'])
  await removeUnusedCopies(join(dir, 'none'), new Set(), () => {})
})

test('parses the semver from claude --version output', () => {
  assert.equal(parseClaudeVersion('2.0.14 (Claude Code)\n'), '2.0.14')
  assert.equal(parseClaudeVersion('claude 1.2.3-beta.4 (Claude Code)'), '1.2.3-beta.4')
  assert.equal(parseClaudeVersion(''), '')
  assert.equal(parseClaudeVersion('version 2.0'), '')
})

// A Managed Work speaks the protocol Dispatcher expects only on the Claude
// Code the Cloud image runs: the package's exact dependency is that version,
// and the one installed with it answers to it.
test('the package bundles the Claude Code the sandbox image pins', async () => {
  const dockerfile = await readFile(join(PACKAGE_ROOT, '..', '..', 'deploy', 'sandbox', 'Dockerfile.base'), 'utf8')
  const pins = [...dockerfile.matchAll(/@anthropic-ai\/claude-code@([0-9][^\s\\]*)/g)].map((match) => match[1])
  assert.equal(pins.length, 1, 'the sandbox image pins one Claude Code')
  const manifest = JSON.parse(await readFile(join(PACKAGE_ROOT, 'package.json'), 'utf8')) as { dependencies: Record<string, string> }
  assert.equal(manifest.dependencies[CLAUDE_CODE_PACKAGE], pins[0])
  assert.equal((await prepareClaudeCode({ copiesDir: join(dir, 'copies') })).version, pins[0], 'the bundled executable runs from its copy')
})
