import assert from 'node:assert/strict'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawn } from 'node:child_process'
import { posixOnly } from './support/platform.ts'
import { afterEach, test } from 'node:test'
import { EXIT_RESTART } from '../src/exit-codes.ts'
import { findInstallation, type Installation } from '../src/upgrade/installation.ts'
import { NpmInstaller } from '../src/upgrade/npm.ts'
import { SUPERVISED_ENV, superviseRuns } from '../src/upgrade/restart.ts'

const dirs: string[] = []
afterEach(async () => {
  while (dirs.length > 0) await rm(dirs.pop() ?? '', { recursive: true, force: true })
})

async function scratch(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'upgrade-install-'))
  dirs.push(dir)
  return dir
}

test('a global install upgrades with the npm beside its node into its own prefix', () => {
  const npm = '/opt/node/lib/node_modules/npm/bin/npm-cli.js'
  assert.deepEqual(findInstallation({
    packageRoot: '/home/a/.npm-global/lib/node_modules/@talgent/computer', node: '/opt/node/bin/node', program: '/home/a/.npm-global/bin/talgent-computer',
    platform: 'linux', exists: (path) => path === npm, writable: () => true,
  }), { node: '/opt/node/bin/node', npmCli: npm, prefix: '/home/a/.npm-global', program: '/home/a/.npm-global/bin/talgent-computer' })

  const windowsNpm = 'C:\\Program Files\\nodejs\\node_modules\\npm\\bin\\npm-cli.js'
  assert.deepEqual(findInstallation({
    packageRoot: 'C:\\Users\\a\\AppData\\Roaming\\npm\\node_modules\\@talgent\\computer', node: 'C:\\Program Files\\nodejs\\node.exe', program: 'C:\\Users\\a\\AppData\\Roaming\\npm\\node_modules\\@talgent\\computer\\dist\\talgent-computer.mjs',
    platform: 'win32', exists: (path) => path === windowsNpm, writable: () => true,
  }), { node: 'C:\\Program Files\\nodejs\\node.exe', npmCli: windowsNpm, prefix: 'C:\\Users\\a\\AppData\\Roaming\\npm', program: 'C:\\Users\\a\\AppData\\Roaming\\npm\\node_modules\\@talgent\\computer\\dist\\talgent-computer.mjs' })

  // npm upgraded into the same prefix serves when none is beside node.
  const prefixNpm = '/usr/local/lib/node_modules/npm/bin/npm-cli.js'
  assert.equal((findInstallation({ packageRoot: '/usr/local/lib/node_modules/@talgent/computer', node: '/usr/bin/node', program: 'p', platform: 'linux', exists: (path) => path === prefixNpm, writable: () => true }) as Installation).npmCli, prefixNpm)
})

test('a checkout, a local install or a node without npm cannot upgrade itself', () => {
  const always = () => true
  assert.match((findInstallation({ packageRoot: '/src/talgent/apps/computer', node: '/usr/bin/node', program: 'p', platform: 'linux', exists: always }) as { unavailable: string }).unavailable, /not from an "npm install -g @talgent\/computer"/)
  assert.match((findInstallation({ packageRoot: '/project/node_modules/@talgent/computer', node: '/usr/bin/node', program: 'p', platform: 'linux', exists: always }) as { unavailable: string }).unavailable, /not in a global npm prefix/)
  assert.match((findInstallation({ packageRoot: '/usr/lib/node_modules/@talgent/computer', node: '/usr/bin/node', program: 'p', platform: 'linux', exists: () => false }) as { unavailable: string }).unavailable, /no npm is installed next to \/usr\/bin\/node/)
})

test('a prefix this user cannot write, as one installed with sudo, cannot upgrade itself', () => {
  const probe = { packageRoot: '/usr/lib/node_modules/@talgent/computer', node: '/usr/bin/node', program: 'p', platform: 'linux' as const, exists: () => true }
  assert.match((findInstallation({ ...probe, writable: (dir) => dir !== '/usr/lib/node_modules' }) as { unavailable: string }).unavailable, /this user cannot write \/usr\/lib\/node_modules, where npm installs it/)
  assert.match((findInstallation({ ...probe, writable: (dir) => dir !== '/usr/bin' }) as { unavailable: string }).unavailable, /cannot write \/usr\/bin/)
})

// A stand-in npm that installs a talgent-computer answering its version
// into the prefix it is given, and refuses 6.6.6 as npm refuses a download
// whose sha512 does not match the registry's.
const FAKE_NPM = `
const { mkdirSync, writeFileSync, appendFileSync } = require('node:fs')
const { join } = require('node:path')
const args = process.argv.slice(2)
appendFileSync(process.env.FAKE_NPM_LOG, JSON.stringify(args) + '\\n')
const prefix = args[args.indexOf('--prefix') + 1]
const version = args.at(-1).split('@').at(-1)
if (version === '6.6.6') {
  process.stderr.write('npm error code EINTEGRITY\\nnpm error sha512-abc integrity checksum failed when using sha512\\n')
  process.exit(1)
}
const dist = join(prefix, 'lib', 'node_modules', '@talgent', 'computer', 'dist')
mkdirSync(dist, { recursive: true })
writeFileSync(join(dist, 'talgent-computer.mjs'), 'process.stdout.write(' + JSON.stringify(version + '\\n') + ')\\n')
`

async function fakeInstallation(): Promise<{ installation: Installation; log: string }> {
  const dir = await scratch()
  const npmCli = join(dir, 'npm-cli.cjs')
  await writeFile(npmCli, FAKE_NPM)
  const prefix = join(dir, 'prefix')
  const program = join(prefix, 'lib', 'node_modules', '@talgent', 'computer', 'dist', 'talgent-computer.mjs')
  const log = join(dir, 'npm.log')
  await writeFile(log, '')
  process.env.FAKE_NPM_LOG = log
  return { installation: { node: process.execPath, npmCli, prefix, program }, log }
}

test('npm installs the release into the prefix and the installed command answers it', async () => {
  const { installation, log } = await fakeInstallation()
  const npm = new NpmInstaller(installation)
  await npm.install('0.2.0')
  assert.equal(await npm.installedVersion(), '0.2.0')
  assert.deepEqual(JSON.parse((await readFile(log, 'utf8')).trim()), ['install', '--global', '--prefix', installation.prefix, '--no-audit', '--no-fund', '@talgent/computer@0.2.0'])

  await assert.rejects(npm.install('6.6.6'), /npm install @talgent\/computer@6\.6\.6 failed \(exit 1\): code EINTEGRITY; sha512-abc integrity checksum failed/)
  assert.equal(await npm.installedVersion(), '0.2.0', 'a refused download leaves the installed release')
})

test('an installed command that does not start fails the check', async () => {
  const { installation } = await fakeInstallation()
  await mkdir(join(installation.program, '..'), { recursive: true })
  await writeFile(installation.program, 'throw new Error("broken")\n')
  await assert.rejects(new NpmInstaller(installation).installedVersion(), /the installed talgent-computer does not start/)
})

test('a supervisor starts the run again while it exits to restart, telling it it is supervised', async () => {
  const dir = await scratch()
  const counter = join(dir, 'runs')
  const script = join(dir, 'run.mjs')
  await writeFile(script, `
import { appendFileSync, readFileSync } from 'node:fs'
appendFileSync(${JSON.stringify(counter)}, process.env.${SUPERVISED_ENV} + '\\n')
const runs = readFileSync(${JSON.stringify(counter)}, 'utf8').trim().split('\\n').length
process.exit(runs < 3 ? ${EXIT_RESTART} : 5)
`)
  const logs: string[] = []
  assert.equal(await superviseRuns({ node: process.execPath, args: [script], env: process.env }, (line) => logs.push(line)), 5)
  assert.deepEqual((await readFile(counter, 'utf8')).trim().split('\n'), ['1', '1', '1'])
  assert.deepEqual(logs, ['restarting', 'restarting'])
})

test('a supervisor rolls back a run that failed, and stops on a stop, one that ended for good, or a failure with nothing to roll back', async () => {
  const dir = await scratch()
  const counter = join(dir, 'runs')
  const script = join(dir, 'run.mjs')
  // Exits with the codes listed in the file, one per run.
  await writeFile(script, `
import { appendFileSync, readFileSync } from 'node:fs'
appendFileSync(${JSON.stringify(counter)}, 'x')
const runs = readFileSync(${JSON.stringify(counter)}, 'utf8').length
process.exit(Number(process.argv[2].split(',')[runs - 1]))
`)
  const failures: number[] = []
  const rolledBack = async (code: number) => {
    failures.push(code)
    return failures.length === 1
  }
  const logs: string[] = []
  assert.equal(await superviseRuns({ node: process.execPath, args: [script, '1,1'], env: process.env }, (line) => logs.push(line), rolledBack), 1)
  assert.deepEqual(failures, [1, 1], 'the first failure rolled back and ran again; the second had nothing to roll back')
  assert.deepEqual(logs, ['restarting into the previous version'])
  for (const code of [0, 3]) {
    await writeFile(counter, '')
    failures.length = 0
    assert.equal(await superviseRuns({ node: process.execPath, args: [script, String(code)], env: process.env }, () => {}, rolledBack), code)
    assert.deepEqual(failures, [], `exit ${code} is no failure`)
  }
})

test('an interrupt at the terminal never cuts npm short', { skip: posixOnly('a terminal interrupt reaches the foreground process group') }, async () => {
  const dir = await scratch()
  const done = join(dir, 'done')
  const parent = join(dir, 'parent.ts')
  // A run that ignores the interrupt, as one finishing an install does,
  // running a slow "npm" through execFileResult.
  await writeFile(parent, `
import { execFileResult } from ${JSON.stringify(new URL('../src/upgrade/npm.ts', import.meta.url).href)}
process.on('SIGINT', () => {})
const result = await execFileResult(process.execPath, ['-e', 'setTimeout(() => require("node:fs").writeFileSync(${JSON.stringify(done)}, "ok"), 800)'], { timeoutMs: 10_000 })
process.stdout.write(String(result.code))
`)
  const child = spawn(process.execPath, [parent], { detached: true, stdio: ['ignore', 'pipe', 'inherit'] })
  let out = ''
  child.stdout.on('data', (chunk: Buffer) => { out += chunk.toString() })
  await new Promise((resolve) => setTimeout(resolve, 400))
  // Ctrl+C: SIGINT to every process of the terminal's foreground group.
  process.kill(-(child.pid ?? 0), 'SIGINT')
  await new Promise((resolve) => child.once('exit', resolve))
  assert.equal(out, '0')
  assert.equal(await readFile(done, 'utf8'), 'ok')
})
