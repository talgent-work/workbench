import { spawn, type ChildProcess } from 'node:child_process'
import { access, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { createServer, type AddressInfo } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { RECONNECT_BASE_ENV } from '../../src/backoff.ts'
import { ExecdClient } from './execd-client.ts'
import { Gateway, type GatewayOptions } from './gateway.ts'

// The execd implementations the conformance suite runs against.
export interface Target {
  name: string
  client: ExecdClient
  // A working directory for test processes.
  cwd: string
  // Where cwd lies on this machine, for a test to hand its processes files.
  hostCwd: string
  // Kills the serving process and starts a new one over the same state,
  // running whileDown in between; only Talgent Computer, whose processes
  // survive it, offers it.
  restart?: (whileDown?: () => Promise<void>) => Promise<void>
  // Drops the connection to the serving process and waits until it
  // reconnected; only Talgent Computer, which dials out, offers it.
  drop?: () => Promise<void>
  close(): Promise<void>
}

// TALGENT_COMPUTER_MAIN runs the suite against a build, dist/talgent-computer.mjs.
const MAIN = process.env.TALGENT_COMPUTER_MAIN || fileURLToPath(new URL('../../src/main.ts', import.meta.url))
const TOKEN = 'conformance-token'

// How long a machine may take to start and connect. Starting src/main.ts
// strips and loads every module, which on a loaded Windows runner takes about
// two seconds; a hook that starts one runs with SETUP_HOOK so the suite's 3s
// per-case cap (package.json) does not cancel it while the machine still
// starts and leaks it.
const MACHINE_START_MS = 15_000
export const SETUP_HOOK = { timeout: MACHINE_START_MS + 5_000 }

// OpenSandbox execd itself, from EXECD_BINARY.
export async function execdTarget(binary: string): Promise<Target> {
  const root = await mkdtemp(join(tmpdir(), 'execd-conformance-'))
  const port = await freePort()
  const log: Buffer[] = []
  const daemon = spawn(binary, [`--port=${port}`], {
    cwd: root,
    env: { PATH: process.env.PATH ?? '', HOME: root, EXECD_ACCESS_TOKEN: TOKEN },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  daemon.stdout.on('data', (chunk: Buffer) => log.push(chunk))
  daemon.stderr.on('data', (chunk: Buffer) => log.push(chunk))
  const client = new ExecdClient(`http://127.0.0.1:${port}`, TOKEN)
  await eventually(async () => (await client.call('GET', '/ping')).status === 200)
  return {
    name: 'execd',
    client,
    cwd: root,
    hostCwd: root,
    close: async () => {
      await stop(daemon)
      await rm(root, { recursive: true, force: true })
    },
  }
}

// Talgent Computer as a separate process behind a stand-in Gateway; machineEnv
// adds to the environment it runs in, and modelProxy opens the Work as one on
// a Model Configuration; reconcile decides each connection's reconciliation.
export async function computerTarget(
  options: { machineEnv?: Record<string, string>; modelProxy?: boolean; reconcile?: GatewayOptions['reconcile'] } = {},
): Promise<Target & { gateway: Gateway; dataDir: string }> {
  const root = await mkdtemp(join(tmpdir(), 'computer-conformance-'))
  const gateway = new Gateway({ workId: 'work-1', accessToken: TOKEN, environment: { CONFORMANCE: 'yes' }, modelProxy: options.modelProxy, reconcile: options.reconcile })
  await gateway.listen()
  const configDir = join(root, 'config', 'talgent-computer')
  await mkdir(configDir, { recursive: true })
  await writeFile(join(configDir, 'credential.json'), JSON.stringify({
    server: gateway.origin,
    computerId: 'computer-1',
    name: 'conformance',
    credential: 'machine-credential',
    credentialExpiresAt: null,
  }))
  // Reconnections after a drop or a restart take milliseconds, not the
  // seconds a machine backs off for in production.
  const env = { ...process.env, [RECONNECT_BASE_ENV]: '10', ...options.machineEnv, XDG_CONFIG_HOME: join(root, 'config'), XDG_DATA_HOME: join(root, 'data') }
  const logs: Buffer[] = []
  const launch = () => {
    const child = spawn(process.execPath, [MAIN, 'run'], { env, stdio: ['ignore', 'pipe', 'pipe'] })
    child.stdout.on('data', (chunk: Buffer) => logs.push(chunk))
    child.stderr.on('data', (chunk: Buffer) => logs.push(chunk))
    return child
  }
  let machine = launch()
  const close = async () => {
    await stop(machine)
    await gateway.close()
    await rm(root, { recursive: true, force: true })
  }
  // A machine that never connects is stopped here: nothing else holds it, and
  // a live child or listening Gateway keeps the test file from ever exiting.
  try {
    await withTimeout(gateway.connected(), MACHINE_START_MS, () => Buffer.concat(logs).toString())
  } catch (error) {
    await close()
    throw error
  }
  return {
    name: 'talgent-computer',
    gateway,
    dataDir: join(root, 'data', 'talgent-computer'),
    client: new ExecdClient(gateway.execdBase, TOKEN),
    cwd: '/workspace/conformance',
    // The Work's /workspace is its root; known once the Work is open.
    get hostCwd() { return join(gateway.workRoot, 'conformance') },
    restart: async (whileDown) => {
      // The Gateway may see the connection close only after the process
      // exited; until then it still answers for the old connection.
      const gone = gateway.disconnected()
      machine.kill('SIGKILL')
      await new Promise((resolve) => machine.once('exit', resolve))
      await gone
      await whileDown?.()
      machine = launch()
      await withTimeout(gateway.connected(), MACHINE_START_MS, () => Buffer.concat(logs).toString())
    },
    drop: async () => {
      const gone = gateway.disconnected()
      gateway.dropMachine()
      await gone
      await withTimeout(gateway.connected(), MACHINE_START_MS, () => Buffer.concat(logs).toString())
    },
    close,
  }
}

async function stop(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return
  const exited = new Promise((resolve) => child.once('exit', resolve))
  child.kill('SIGKILL')
  await exited
}

async function withTimeout<T>(promise: Promise<T>, ms: number, describe: () => string): Promise<T> {
  let timer: NodeJS.Timeout | undefined
  try {
    return await Promise.race([
      promise,
      new Promise<never>((_resolve, reject) => {
        timer = setTimeout(() => reject(new Error(`timed out; machine log:\n${describe()}`)), ms)
      }),
    ])
  } finally {
    clearTimeout(timer)
  }
}

export async function eventually(check: () => Promise<boolean>, timeoutMs = 10_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    try {
      if (await check()) return
    } catch {
      // Not yet.
    }
    if (Date.now() > deadline) throw new Error('condition not met in time')
    await new Promise((resolve) => setTimeout(resolve, 25))
  }
}

export async function exists(path: string): Promise<boolean> {
  try {
    await access(path)
    return true
  } catch {
    return false
  }
}

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer()
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address() as AddressInfo
      server.close(() => resolve(port))
    })
  })
}
