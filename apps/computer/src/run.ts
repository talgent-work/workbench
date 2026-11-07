import { homedir } from 'node:os'
import { join } from 'node:path'
import { reconnectBackoff } from './backoff.ts'
import { ClaudeCodeMissingError, PACKAGE_ROOT, prepareClaudeCode, removeUnusedCopies, type BundledClaudeCode } from './claude-code.ts'
import type { CliIO } from './cli.ts'
import { runConnection } from './connection.ts'
import { endEveryWorkProcess } from './end-work-processes.ts'
import { WorkInputs } from './execd/inputs.ts'
import { ProcessTable } from './execd/processes.ts'
import { ExecdService } from './execd/service.ts'
import { SHIM_ARG } from './execd/shim.ts'
import { Works } from './execd/works.ts'
import {
  EXIT_COMPUTER_REMOVED, EXIT_CREDENTIAL_REJECTED, EXIT_ERROR, EXIT_OK, EXIT_REPLACED, EXIT_RESTART,
} from './exit-codes.ts'
import { Fence } from './fence.ts'
import { forgetRun, recordRun, RUN_RECORD_PATH } from './run-record.ts'
import { HeldCredential } from './held-credential.ts'
import { thisProgram } from './paths.ts'
import { CredentialStore } from './proxy/credentials.ts'
import { LocalProxy } from './proxy/server.ts'
import { sandboxHeader } from './sandbox.ts'
import { serviceManager } from './service/command.ts'
import type { Credential } from './store.ts'
import { Tunnel } from './tunnel.ts'
import { findInstallation, runningNode, type Installation } from './upgrade/installation.ts'
import { InstallLock } from './upgrade/lock.ts'
import { INSTALL_TIMEOUT_MS, NpmInstaller } from './upgrade/npm.ts'
import { readRecord } from './upgrade/record.ts'
import { superviseRuns, supervised } from './upgrade/restart.ts'
import { SelfUpgrade } from './upgrade/self-upgrade.ts'
import { COMPUTER_VERSION } from './version.ts'

// "talgent-computer run": the machine's run, from preparing what Work
// processes use and fencing them, through upgrading itself, to serving
// Works over the connection; and the restart into an installed upgrade.

// Runs this registered machine at url until the connection ends, and
// answers the exit code.
export async function runMachine(io: CliIO, credential: Credential, url: string): Promise<number> {
  const log = (message: string) => io.err(`${new Date().toISOString()} ${message}`)
  const { dataDir } = io.paths
  const controller = new AbortController()
  let restarting = false
  const upgrade = selfUpgrade(dataDir, log, () => {
    if (controller.signal.aborted) return
    restarting = true
    controller.abort()
  })
  const copiesDir = join(dataDir, 'claude-code')
  const claudeCode = await prepareClaudeCode({ copiesDir }).catch((error: unknown) => {
    if (error instanceof ClaudeCodeMissingError) return error
    throw error
  })
  const processes = processTable(dataDir, claudeCode instanceof Error ? [] : [claudeCode.binDir])
  // Ends the Work processes the platform stops once it lost reach of this
  // machine. It starts before anything that may install, and fences
  // throughout: rolling back while offline takes as long as npm does.
  const fence = newFence(dataDir, processes, log)
  await fence.start()
  const runFile = runRecordFile(dataDir)
  await recordRun(runFile).catch((error: unknown) => log(`cannot record this run: ${error instanceof Error ? error.message : String(error)}`))
  const stop = () => controller.abort()
  process.once('SIGINT', stop)
  process.once('SIGTERM', stop)
  let pruning: NodeJS.Timeout | undefined
  try {
    if (await upgrade.start() === 'restart') return EXIT_RESTART
    if (claudeCode instanceof Error) {
      io.err(claudeCode.message)
      return await upgrade.failedToStart(claudeCode.message) ? EXIT_RESTART : EXIT_ERROR
    }
    log(`Work processes run Claude Code ${claudeCode.version}`)
    // Copies of earlier versions go once no Work process runs on them.
    const prune = async () => removeUnusedCopies(copiesDir, new Set([claudeCode.binDir, ...await processes.inUse()]), log)
      .catch((error: unknown) => log(`cannot look for unused Claude Code copies: ${error instanceof Error ? error.message : String(error)}`))
    await prune()
    pruning = setInterval(() => void prune(), PRUNE_EVERY_MS)
    pruning.unref()
    if (controller.signal.aborted) {
      log('stopped')
      return EXIT_OK
    }
    return await connect({ io, log, url, credential, claudeCode, processes, fence, upgrade, signal: controller.signal, restarting: () => restarting })
  } finally {
    clearInterval(pruning)
    await upgrade.settle()
    await fence.stop()
    await forgetRun(runFile).catch(() => {})
    process.off('SIGINT', stop)
    process.off('SIGTERM', stop)
  }
}

interface Connecting {
  io: CliIO
  log: (message: string) => void
  url: string
  credential: Credential
  claudeCode: BundledClaudeCode
  processes: ProcessTable
  fence: Fence
  upgrade: SelfUpgrade
  signal: AbortSignal
  restarting: () => boolean
}

// Serves the Works on this machine through the connection until it ends.
async function connect(options: Connecting): Promise<number> {
  const { io, log, url, credential, claudeCode, processes, fence, upgrade } = options
  const { dataDir } = io.paths
  const works = new Works({ worksDir: join(dataDir, 'works'), recordsDir: join(dataDir, 'state', 'works') })
  // Work credentials live in Talgent Computer's state, outside every Work root.
  const credentials = new CredentialStore({ dir: join(dataDir, 'state', 'credentials') })
  const proxy = new LocalProxy({
    portFile: join(dataDir, 'state', 'proxy.json'),
    access: {
      tokenValid: (workId, token) => processes.proxyTokenValid(workId, token),
      credentials: (workId) => credentials.get(workId),
      modelProxy: (workId) => works.modelProxy(workId),
      workRoot: (workId) => works.root(workId),
    },
    log,
  })
  await proxy.start()
  log(`local proxy listening on ${proxy.origin()}`)
  const service = new ExecdService({
    works,
    processes,
    claudeBinDir: claudeCode.binDir,
    credentials,
    inputs: new WorkInputs({ recordsDir: join(dataDir, 'state', 'inputs'), credentials, log }),
    proxyOrigin: () => proxy.origin(),
    baseEnv: process.env,
    home: homedir(),
    log,
  })

  const held = new HeldCredential(io.paths, credential)
  log(`connecting "${credential.name}" to ${new URL(url).host}`)
  try {
    const outcome = await runConnection({
      url,
      headers: () => ({
        Authorization: `Bearer ${held.secret()}`,
        'X-Talgent-Computer-Version': COMPUTER_VERSION,
        'X-Talgent-Claude-Code-Version': claudeCode.version,
        // Read on every connection, so installing what the sandbox needs
        // takes effect on the next one.
        'X-Talgent-Claude-Code-Sandbox': sandboxHeader({ platform: process.platform, path: process.env.PATH }),
      }),
      signal: options.signal,
      log,
      backoff: reconnectBackoff(process.env),
      relay: (link) => new Tunnel(service, link, log),
      heard: () => void fence.heard(),
      // The welcome precedes every relayed call, so the connection's
      // reconciliation round begins before anything is relayed: one the
      // platform reconciles holds starts back until it decided what still
      // runs here.
      welcomed: (welcome) => {
        service.reconciliation.begin(welcome.reconciles)
        upgrade.announced({ target: welcome.targetVersion, minimum: welcome.minimumVersion })
      },
      credentialIssued: (issued) => held.replace(issued),
    })
    switch (outcome.kind) {
      case 'stopped':
        if (options.restarting()) return EXIT_RESTART
        log('stopped')
        return EXIT_OK
      case 'credential_rejected':
        await endEveryWorkProcess(processes, 'credential_rejected', log)
        io.err('Talgent rejected this Computer\'s credential (expired or revoked).')
        io.err('Run "talgent-computer register --force" to register this machine again.')
        return EXIT_CREDENTIAL_REJECTED
      case 'computer_removed':
        await endEveryWorkProcess(processes, 'computer_removed', log)
        io.err('This Computer was removed from Talgent.')
        io.err('Run "talgent-computer register --force" to add this machine again.')
        return EXIT_COMPUTER_REMOVED
      case 'replaced':
        io.err('Another "talgent-computer run" with this credential connected and took over.')
        return EXIT_REPLACED
    }
  } finally {
    await proxy.close()
  }
}

// The npm installation this command runs from.
export function thisInstallation(): Installation | { unavailable: string } {
  return findInstallation({ packageRoot: PACKAGE_ROOT, node: runningNode(), program: thisProgram() })
}

// How often copies of Claude Code no Work process uses any more are looked for.
const PRUNE_EVERY_MS = 60 * 60_000
// A process may hold the install lock for a failed install and the
// reinstall after it.
const INSTALL_LOCK_MS = 2 * INSTALL_TIMEOUT_MS + 60_000

function selfUpgrade(dataDir: string, log: (message: string) => void, restart: () => void): SelfUpgrade {
  const installation = thisInstallation()
  return new SelfUpgrade({
    version: COMPUTER_VERSION,
    recordFile: upgradeRecordFile(dataDir),
    installer: 'unavailable' in installation ? installation : new NpmInstaller(installation),
    lock: new InstallLock({ file: join(dataDir, 'state', 'upgrade.lock'), staleAfterMs: INSTALL_LOCK_MS, waitMs: INSTALL_LOCK_MS }),
    restart,
    log,
  })
}

function runRecordFile(dataDir: string): string {
  return join(dataDir, ...RUN_RECORD_PATH)
}

function upgradeRecordFile(dataDir: string): string {
  return join(dataDir, 'state', 'upgrade.json')
}

// The Work processes on this machine; each run started records what it uses.
function processTable(dataDir: string, uses: string[]): ProcessTable {
  return new ProcessTable({ stateRoot: join(dataDir, 'state', 'processes'), shim: [process.execPath, thisProgram(), SHIM_ARG], uses })
}

function newFence(dataDir: string, processes: ProcessTable, log: (message: string) => void): Fence {
  return new Fence({ stateFile: join(dataDir, 'state', 'fence.json'), processes, log })
}

// A run that ended to restart into the installed version is started again:
// by its starter when it is supervised, else by this process, which then
// stays in the terminal as the supervisor.
export async function restartWhenAsked(code: number, io: CliIO): Promise<number> {
  if (code !== EXIT_RESTART || supervised(process.env)) return code
  const log = (message: string) => io.err(`${new Date().toISOString()} ${message}`)
  const outdated = await outdatedService(io)
  if (outdated !== undefined && outdated.pid === process.pid) log(`${OUTDATED_SERVICE}; until then this run stays as the supervisor of the next`)
  return await superviseRuns({ node: process.execPath, args: [thisProgram(), 'run'], env: process.env }, log, (exitCode) => rollBackFenced(io.paths.dataDir, exitCode, log))
}

// A supervised run that failed while on probation is rolled back here,
// with Work processes fenced meanwhile as a run fences them.
async function rollBackFenced(dataDir: string, code: number, log: (message: string) => void): Promise<boolean> {
  if (readRecord(upgradeRecordFile(dataDir)).pending === undefined) return false
  const fence = newFence(dataDir, processTable(dataDir, []), log)
  await fence.start()
  try {
    return await selfUpgrade(dataDir, log, () => {}).runFailed(`it exited with code ${code}`)
  } finally {
    await fence.stop()
  }
}

export const OUTDATED_SERVICE = 'the background service was installed by an earlier Talgent Computer and does not restart into upgrades; run "talgent-computer service install" again'

// The installed background service when it predates self-upgrade; never
// fails, as a machine without a service manager has none.
export async function outdatedService(io: CliIO): Promise<{ pid?: number } | undefined> {
  try {
    const state = await serviceManager(io.serviceHost(), io.paths).status()
    return state.installed && !state.supervisesUpgrades ? { pid: state.pid } : undefined
  } catch {
    return undefined
  }
}
