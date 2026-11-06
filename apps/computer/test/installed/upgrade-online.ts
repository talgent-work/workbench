// Self-upgrade, on the machine this runs on, of a talgent-computer npm
// installed from a stand-in registry into a scratch prefix:
//   npm pack && node test/installed/upgrade-online.ts
// A run in the terminal, connected to a stand-in Gateway, holds a Work
// process. The Gateway then announces the next release as Target: the run
// installs it with npm, restarts into it and comes back online as it, and
// the Work process continues throughout. A release whose download fails
// npm's integrity check leaves the machine on the version it runs.
import assert from 'node:assert/strict'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { copyFile, mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import { ExecdClient, output, Pipe } from '../support/execd-client.ts'
import { Gateway } from '../support/gateway.ts'
import { Registry } from './registry.ts'

const TOKEN = 'upgrade-token'
const packageDir = new URL('../../', import.meta.url)
const manifest = JSON.parse(await readFile(new URL('package.json', packageDir), 'utf8')) as Record<string, unknown> & { version: string }
const windows = process.platform === 'win32'
const [major, minor, patch] = manifest.version.split('.').map(Number)
const next = `${major}.${minor}.${patch + 1}`
const corrupt = `${major}.${minor}.${patch + 2}`

// Through any symbolic link, as macOS's /var is one: the installed command
// reports the prefix it really runs from.
const scratch = await realpath(await mkdtemp(join(tmpdir(), 'installed-upgrade-')))
const home = join(scratch, 'home')
const prefix = join(scratch, 'prefix')
const registry = new Registry()
await registry.listen()
const gateway = new Gateway({ workId: 'work-1', accessToken: TOKEN })
await gateway.listen()
// npm keeps the person's own configuration and cache under the scratch
// home; only the @talgent scope comes from the stand-in registry, through a
// global configuration file, which a shell passes on as no variable named
// with "@" would be.
const npmConfig = (key: string) => execFileSync('npm', ['config', 'get', key], { encoding: 'utf8', shell: windows }).trim()
const globalConfig = join(scratch, 'npmrc')
await writeFile(globalConfig, `@talgent:registry=${registry.origin}/\n`)
// npm run hands its own configuration to scripts as npm_config_*; the
// machine's npm reads only what is set here.
const inherited = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^npm_config_/i.test(key)))
const env: NodeJS.ProcessEnv = {
  ...inherited, HOME: home, NPM_CONFIG_USERCONFIG: npmConfig('userconfig'), NPM_CONFIG_CACHE: npmConfig('cache'), NPM_CONFIG_GLOBALCONFIG: globalConfig,
}
// Windows names it Path; the scratch prefix's command comes first.
const pathKey = Object.keys(env).find((key) => key.toUpperCase() === 'PATH') ?? 'PATH'
env[pathKey] = [windows ? prefix : join(prefix, 'bin'), env[pathKey]].join(delimiter)
delete env.XDG_CONFIG_HOME
delete env.XDG_DATA_HOME
if (windows) {
  env.APPDATA = join(home, 'AppData', 'Roaming')
  env.LOCALAPPDATA = join(home, 'AppData', 'Local')
}
let machine: ChildProcess | undefined
const log: Buffer[] = []

function command(line: string): ChildProcess {
  const child = spawn(`talgent-computer ${line}`, { shell: true, env, detached: !windows, stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout?.on('data', (chunk: Buffer) => log.push(chunk))
  child.stderr?.on('data', (chunk: Buffer) => log.push(chunk))
  return child
}

function exited(child: ChildProcess): Promise<number | null> {
  return new Promise((resolve) => child.once('exit', (code) => resolve(code)))
}

// What the installed command answers to --version, asked again until it
// answers the expected version or the caller's bound ends.
async function installedVersion(expected: string): Promise<string> {
  for (;;) {
    const version = command('--version')
    const answer: Buffer[] = []
    version.stdout?.on('data', (chunk: Buffer) => answer.push(chunk))
    const code = await exited(version)
    const said = Buffer.concat(answer).toString().trim()
    if (code === 0 && said === expected) return said
    await new Promise((resolve) => setTimeout(resolve, 1000))
  }
}

function within<T>(promise: Promise<T>, ms: number, what: string): Promise<T> {
  return Promise.race([promise, new Promise<never>((_resolve, reject) => setTimeout(() => reject(new Error(`${what} timed out:\n${Buffer.concat(log).toString()}`)), ms).unref())])
}

async function logged(pattern: RegExp, ms: number, what: string): Promise<void> {
  await within((async () => {
    while (!pattern.test(Buffer.concat(log).toString())) await new Promise((resolve) => setTimeout(resolve, 200))
  })(), ms, what)
}

function stop(child: ChildProcess | undefined): void {
  if (child?.pid === undefined || child.exitCode !== null) return
  try {
    if (windows) execFileSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' })
    else process.kill(-child.pid, 'SIGTERM')
  } catch {
    // It ended on its own meanwhile.
  }
}

// The packed package as the next release: its bundle answers that version.
async function nextRelease(version: string): Promise<Buffer> {
  const stage = join(scratch, `stage-${version}`)
  await mkdir(join(stage, 'dist'), { recursive: true })
  await writeFile(join(stage, 'package.json'), JSON.stringify({ ...manifest, version }, null, 2))
  await copyFile(new URL('dist/talgent-computer.mjs', packageDir), join(stage, 'dist', 'talgent-computer.mjs'))
  const bundle = await readFile(new URL('dist/computer.mjs', packageDir), 'utf8')
  const inlined = `version: ${JSON.stringify(manifest.version)},`
  assert.equal(bundle.split(inlined).length, 2, 'the bundle inlines its version once')
  await writeFile(join(stage, 'dist', 'computer.mjs'), bundle.replace(inlined, `version: ${JSON.stringify(version)},`))
  execFileSync('npm', ['pack', '--pack-destination', scratch], { cwd: stage, shell: windows, stdio: 'ignore' })
  return readFile(join(scratch, `talgent-computer-${version}.tgz`))
}

try {
  const packed = await readFile(new URL(`talgent-computer-${manifest.version}.tgz`, packageDir))
  registry.releases.set(manifest.version, { manifest, tarball: packed })
  const upgraded = await nextRelease(next)
  registry.releases.set(next, { manifest, tarball: upgraded })
  // Declared bytes no cache holds, so npm downloads and checks the tarball.
  registry.releases.set(corrupt, { manifest, tarball: upgraded, declared: Buffer.from(`not the ${corrupt} tarball`) })
  // Asynchronously, as this process serves the registry npm reads.
  const installing = spawn('npm', ['install', '--global', '--prefix', prefix, '--no-audit', '--no-fund', `@talgent/computer@${manifest.version}`], { env, shell: windows, stdio: 'inherit' })
  assert.equal(await within(exited(installing), 600_000, 'npm install'), 0)

  assert.equal(await within(exited(command(`register --server ${gateway.origin} --name upgrade-check`)), 60_000, 'register'), 0, Buffer.concat(log).toString())
  const status = command('status')
  assert.equal(await within(exited(status), 30_000, 'status'), 0)
  assert.ok(Buffer.concat(log).toString().includes(`into ${prefix}`), 'the installed command upgrades into its own prefix')

  machine = command('run')
  await within(gateway.connected(), 120_000, 'run')
  assert.equal(gateway.headers.at(-1)?.['x-talgent-computer-version'], manifest.version)
  const client = new ExecdClient(gateway.execdBase, TOKEN)
  // The Work process runs the bundled Claude Code from its PATH and keeps one
  // running through the upgrade (an MCP server waiting on its input), so an
  // install replacing an executable a Work runs fails, as Windows refuses to.
  const claudeCode = (manifest.dependencies as Record<string, string>)['@anthropic-ai/claude-code']
  // Its input ends when the sleep feeding it is killed, after the upgrade; sh
  // names its own pid portably, as macOS's bash 3.2 has no BASHPID.
  const holder = 'claude-holder.pid'
  const id = await client.create('/workspace', `claude --version; sh -c 'echo $$ > ${holder}; exec sleep 600' | claude mcp serve >/dev/null 2>&1 & sleep 2; echo started; read line; kill $(cat ${holder}); wait; echo "got-$line"`)
  const first = await client.attach(id, 0)
  assert.ok(first instanceof Pipe, JSON.stringify(first))
  const started = `${claudeCode} (Claude Code)\nstarted\n`
  await within(first.until((_frame, seen) => output(seen, 'stdout') === started), 30_000, 'the Work process')

  // The next connection announces the next release.
  gateway.targetVersion = next
  const reconnected = gateway.disconnected()
  gateway.restartMachine()
  await reconnected
  await within(gateway.connected(), 60_000, 'reconnecting')
  const online = gateway.disconnected()
  await logged(new RegExp(`upgrading Talgent Computer ${manifest.version} to ${next}`), 30_000, 'the upgrade starting')
  await within(online, 300_000, 'the run restarting into the upgrade')
  await within(gateway.connected(), 120_000, 'the upgrade coming online')
  assert.equal(gateway.headers.at(-1)?.['x-talgent-computer-version'], next, 'the machine runs the Target Version now')
  await logged(new RegExp(`upgraded to Talgent Computer ${next.replace(/\./g, '\\.')}`), 30_000, 'the upgrade confirming')

  assert.equal((await client.status(id)).running, true, 'the Work process ran on through the upgrade')
  const second = await client.attachWhenFree(id, Buffer.byteLength(started))
  second.stdin('on\n')
  const rest = await within(second.until((frame) => frame.type === 'exit'), 30_000, 'the Work process exit')
  assert.equal(output(rest, 'stdout'), 'got-on\n')
  assert.deepEqual(rest.at(-1), { type: 'exit', exit_code: 0 })
  assert.equal((await client.call('DELETE', `/pty/${id}`)).status, 200)
  console.log(`Talgent Computer upgraded itself from ${manifest.version} to ${next} while a Work process ran on ${process.platform}`)

  // A release whose download does not match its integrity is refused.
  gateway.targetVersion = corrupt
  const dropped = gateway.disconnected()
  gateway.restartMachine()
  await dropped
  await within(gateway.connected(), 60_000, 'reconnecting')
  await logged(new RegExp(`upgrading to ${corrupt.replace(/\./g, '\\.')} failed: .*EINTEGRITY`), 300_000, 'the corrupt release being refused')
  // npm on Windows removes the installed package before the failed install
  // gives up; the run then reinstalls the version it runs. The installed
  // command answers once that is back.
  assert.equal(await within(installedVersion(next), 120_000, 'the installed version after the refusal'), next, 'the refused release left the installed one')
  assert.equal(gateway.headers.at(-1)?.['x-talgent-computer-version'], next)
  console.log(`a release failing npm's integrity check left Talgent Computer ${next} in place on ${process.platform}`)
} catch (error) {
  // Said before cleanup, which may itself hang on a machine in a bad state.
  console.error(error)
  console.error(Buffer.concat(log).toString())
  process.exitCode = 1
  // Cleanup gets two minutes; a machine in a bad state must not hang CI.
  setTimeout(() => process.exit(1), 120_000).unref()
} finally {
  stop(machine)
  await gateway.close()
  await registry.close()
  await rm(scratch, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 })
}
