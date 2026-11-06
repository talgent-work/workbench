// Install, register and online, on the machine this runs on, with the
// talgent-computer command npm installed from the package tarball:
//   npm pack && npm install -g ./talgent-computer-<version>.tgz
//   node test/installed/install-register-online.ts
// It registers against a stand-in Gateway that approves the device code,
// connects, and runs one Work process through the machine's process host.
// Then it installs the background service, which must come online, and
// uninstalls it, which must end the connection. The service is the person's
// own (systemd --user, a LaunchAgent, a logon task) and is removed at the end.
// The files land in this platform's default places under a scratch home.
import assert from 'node:assert/strict'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { resolvePaths } from '../../src/paths.ts'
import { ExecdClient, output, Pipe } from '../support/execd-client.ts'
import { Gateway, MACHINE_CREDENTIAL } from '../support/gateway.ts'

const TOKEN = 'installed-token'
const manifest = JSON.parse(await readFile(new URL('../../package.json', import.meta.url), 'utf8')) as { version: string; dependencies: Record<string, string> }
const windows = process.platform === 'win32'

const scratch = await mkdtemp(join(tmpdir(), 'installed-'))
const home = join(scratch, 'home')
const env: NodeJS.ProcessEnv = { ...process.env, HOME: home }
delete env.XDG_CONFIG_HOME
delete env.XDG_DATA_HOME
if (windows) {
  env.APPDATA = join(home, 'AppData', 'Roaming')
  env.LOCALAPPDATA = join(home, 'AppData', 'Local')
}
const gateway = new Gateway({ workId: 'work-1', accessToken: TOKEN })
await gateway.listen()
let machine: ChildProcess | undefined
let serviceInstalled = false
const log: Buffer[] = []

// Runs the installed command as a person types it.
function command(line: string): ChildProcess {
  const child = spawn(`talgent-computer ${line}`, { shell: true, env, detached: !windows, stdio: ['ignore', 'pipe', 'pipe'] })
  child.stdout?.on('data', (chunk: Buffer) => log.push(chunk))
  child.stderr?.on('data', (chunk: Buffer) => log.push(chunk))
  return child
}

// Runs a command to its end; its exit code and what it printed.
async function finished(line: string): Promise<{ code: number | null; output: string }> {
  const child = command(line)
  const output: Buffer[] = []
  child.stdout?.on('data', (chunk: Buffer) => output.push(chunk))
  child.stderr?.on('data', (chunk: Buffer) => output.push(chunk))
  const code = await within(exited(child), 60_000, line)
  return { code, output: Buffer.concat(output).toString() }
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

function exited(child: ChildProcess): Promise<number | null> {
  return new Promise((resolve) => child.once('exit', (code) => resolve(code)))
}

function within<T>(promise: Promise<T>, ms: number, what: string): Promise<T> {
  return Promise.race([promise, new Promise<never>((_resolve, reject) => setTimeout(() => reject(new Error(`${what} timed out:\n${Buffer.concat(log).toString()}`)), ms).unref())])
}

try {
  const version = command('--version')
  assert.equal(await within(exited(version), 30_000, '--version'), 0)
  assert.equal(Buffer.concat(log).toString().trim(), manifest.version, 'the installed command is this package')

  const registered = await within(exited(command(`register --server ${gateway.origin} --name installed-check`)), 60_000, 'register')
  assert.equal(registered, 0, Buffer.concat(log).toString())
  assert.equal(gateway.machineName, 'installed-check')
  const paths = resolvePaths(env, home)
  assert.ok(existsSync(paths.credentialFile), `the credential is saved at ${paths.credentialFile}`)

  machine = command('run')
  await within(gateway.connected(), 120_000, 'run')
  const headers = gateway.headers.at(-1) ?? {}
  assert.equal(headers.authorization, `Bearer ${MACHINE_CREDENTIAL}`)
  assert.equal(headers['x-talgent-computer-version'], manifest.version)
  assert.equal(headers['x-talgent-claude-code-version'], manifest.dependencies['@anthropic-ai/claude-code'], 'the bundled Claude Code runs')

  const client = new ExecdClient(gateway.execdBase, TOKEN)
  // The Work process runs the bundled Claude Code as `claude` from its PATH:
  // the copy in Talgent Computer's data directory, never the package's.
  const claudeCode = manifest.dependencies['@anthropic-ai/claude-code']
  const id = await client.create('/workspace', 'echo "online-$((40 + 2))"; command -v claude; claude --version; read line; echo "got-$line"')
  const pipe = await client.attach(id, 0)
  assert.ok(pipe instanceof Pipe, JSON.stringify(pipe))
  const first = await within(pipe.until((_frame, seen) => output(seen, 'stdout').includes('(Claude Code)\n')), 30_000, 'the Work process')
  const [online, claude, answered] = output(first, 'stdout').split('\n')
  assert.equal(online, 'online-42')
  assert.match(claude, new RegExp(`claude-code/${claudeCode.replace(/\./g, '\\.')}/claude(\\.exe)?$`), 'claude on the Work\'s PATH is the per-version copy')
  assert.equal(answered, `${claudeCode} (Claude Code)`)
  pipe.stdin('stdin\n')
  const rest = await within(pipe.until((frame) => frame.type === 'exit'), 30_000, 'the Work process exit')
  assert.equal(output(rest, 'stdout'), 'got-stdin\n')
  assert.deepEqual(rest.at(-1), { type: 'exit', exit_code: 0 })
  assert.equal((await client.call('DELETE', `/pty/${id}`)).status, 200)
  console.log(`installed Talgent Computer ${manifest.version} registered, came online and ran a Work process on ${process.platform}`)

  const runEnded = gateway.disconnected()
  stop(machine)
  await within(runEnded, 30_000, 'the run stopping')
  assert.equal((await finished('service status')).code, 8, 'no service before install')
  serviceInstalled = true
  const installed = await finished('service install')
  assert.equal(installed.code, 0, installed.output)
  await within(gateway.connected(), 120_000, 'the background service coming online')
  const running = await finished('service status')
  assert.equal(running.code, 0, running.output)
  assert.match(running.output, /Service: running/)
  const serviceEnded = gateway.disconnected()
  const uninstalled = await finished('service uninstall')
  assert.equal(uninstalled.code, 0, uninstalled.output)
  serviceInstalled = false
  await within(serviceEnded, 60_000, 'the uninstalled service stopping')
  assert.equal((await finished('service status')).code, 8, 'no service after uninstall')
  console.log(`the background service came online and stopped when uninstalled on ${process.platform}`)
} catch (error) {
  // Said before cleanup, which may itself hang on a machine in a bad state.
  console.error(error)
  console.error(Buffer.concat(log).toString())
  process.exitCode = 1
  // Cleanup gets two minutes; a machine in a bad state must not hang CI.
  setTimeout(() => process.exit(1), 120_000).unref()
} finally {
  stop(machine)
  if (serviceInstalled) await finished('service uninstall').catch(() => {})
  await gateway.close()
  await rm(scratch, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 })
}
