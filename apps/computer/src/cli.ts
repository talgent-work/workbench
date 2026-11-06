import { hostname } from 'node:os'
import { parseArgs, type ParseArgsOptionsConfig } from 'node:util'
import { setTimeout as sleep } from 'node:timers/promises'
import { connectUrl } from './connection.ts'
import { resolvePaths, type Paths } from './paths.ts'
import { awaitRegistration, RegistrationExpiredError, startRegistration } from './registration.ts'
import { outdatedService, OUTDATED_SERVICE, restartWhenAsked, runMachine, thisInstallation } from './run.ts'
import { CredentialExistsError, readConfig, readCredential, writeCredential } from './store.ts'
import {
  EXIT_ERROR, EXIT_NOT_REGISTERED, EXIT_OK, EXIT_REGISTRATION_EXPIRED, EXIT_SERVICE_NOT_INSTALLED, EXIT_USAGE, exitReason,
} from './exit-codes.ts'
import { currentServiceHost, serviceManager, serviceSpec, type ServiceHost } from './service/command.ts'
import { COMPUTER_VERSION } from './version.ts'

const HELP = `Talgent Computer ${COMPUTER_VERSION}
Registers this machine as a Managed Computer and keeps it connected to Talgent.

Usage:
  talgent-computer register [--name <name>] [--server <origin>] [--force]
      Approve this machine in the browser and save its credential.
      --name    Computer name (default: this machine's host name)
      --server  Talgent origin, as the Computers page shows it
                (default: "server" in the configuration file)
      --force   Replace an existing credential with a new Computer
  talgent-computer run
      Stay connected to Talgent and run its Works here until interrupted.
      Work processes keep running when it stops; the next run picks them up.
      It upgrades itself with npm to the release Talgent runs, and restarts
      into it without ending Work processes.
  talgent-computer status
      Show the registered Computer.
  talgent-computer service install|status|uninstall
      Run "talgent-computer run" as your own background service, started at
      login and again after it exits (launchd on macOS, systemd --user on
      Linux, a logon task on Windows). It takes its settings from the
      configuration file, as run does.

Options:
  -h, --help     Show this help
  -v, --version  Show the version

Exit codes: 0 ok, 1 error, 2 usage, 3 credential rejected, 4 Computer removed, 7 replaced by another run,
5 not registered, 6 registration expired, 8 service not installed, 9 restarting into another version.
`

export interface CliIO {
  out: (line: string) => void
  err: (line: string) => void
  paths: Paths
  serviceHost: () => ServiceHost
}

export async function main(argv: string[], io: CliIO = defaultIO()): Promise<number> {
  const [command, ...rest] = argv
  if (command === undefined || command === '-h' || command === '--help' || command === 'help') {
    io.out(HELP)
    return command === undefined ? EXIT_USAGE : EXIT_OK
  }
  if (command === '-v' || command === '--version') {
    io.out(COMPUTER_VERSION)
    return EXIT_OK
  }
  try {
    switch (command) {
      case 'register': return await register(rest, io)
      case 'run': return await restartWhenAsked(await run(rest, io), io)
      case 'status': return await status(rest, io)
      case 'service': return await service(rest, io)
      default:
        io.err(`unknown command "${command}"; see talgent-computer --help`)
        return EXIT_USAGE
    }
  } catch (error) {
    if (error instanceof UsageError) {
      io.err(`${error.message}; see talgent-computer --help`)
      return EXIT_USAGE
    }
    io.err(`error: ${error instanceof Error ? error.message : String(error)}`)
    return EXIT_ERROR
  }
}

async function register(args: string[], io: CliIO): Promise<number> {
  const { values } = parse(args, {
    name: { type: 'string' },
    server: { type: 'string' },
    force: { type: 'boolean', default: false },
  })
  const force = values.force === true
  const existing = await readCredential(io.paths)
  if (existing !== undefined && !force) {
    io.err(`This machine is already registered as "${existing.name}" (${existing.computerId}).`)
    io.err('Run "talgent-computer register --force" to register it again as a new Computer.')
    return EXIT_ERROR
  }
  const configured = (await readConfig(io.paths)).server
  const serverInput = typeof values.server === 'string' ? values.server : configured
  if (serverInput === undefined) throw new UsageError('no server configured; pass --server <origin>')
  const server = normalizeServer(serverInput)
  const name = (typeof values.name === 'string' ? values.name : hostname()).trim()
  if (name === '') throw new UsageError('--name must not be empty')

  const deps = { fetch, sleep: (ms: number) => sleep(ms), now: Date.now }
  const started = await startRegistration(deps, server, name)
  io.out('')
  io.out(`  Open ${started.verificationUriComplete}`)
  io.out(`  and confirm the code ${started.userCode}`)
  io.out('')
  io.out(`Waiting for approval (expires in ${Math.ceil(started.expiresInSeconds / 60)} min)...`)
  let credential
  try {
    credential = await awaitRegistration(deps, server, started)
  } catch (error) {
    if (error instanceof RegistrationExpiredError) {
      io.err('The registration expired or was already used. If this machine now shows on your Computers page as never connected, it registered but its credential was lost; run "talgent-computer register" again.')
      return EXIT_REGISTRATION_EXPIRED
    }
    throw error
  }
  try {
    await writeCredential(io.paths, credential, { force })
  } catch (error) {
    if (error instanceof CredentialExistsError) {
      io.err(`Another credential appeared at ${error.path} while waiting; the new Computer was not saved.`)
      return EXIT_ERROR
    }
    throw error
  }
  io.out(`Registered this machine as "${credential.name}".`)
  io.out('Run "talgent-computer run" to connect it.')
  return EXIT_OK
}

async function run(args: string[], io: CliIO): Promise<number> {
  parse(args, {})
  const credential = await readCredential(io.paths)
  if (credential === undefined) {
    io.err('This machine is not registered. Run "talgent-computer register" first.')
    return EXIT_NOT_REGISTERED
  }
  return await runMachine(io, credential, connectUrl(normalizeServer(credential.server)))
}

async function status(args: string[], io: CliIO): Promise<number> {
  parse(args, {})
  const credential = await readCredential(io.paths)
  if (credential === undefined) {
    io.out('Not registered. Run "talgent-computer register".')
    return EXIT_NOT_REGISTERED
  }
  io.out(`Server:   ${credential.server}`)
  io.out(`Computer: ${credential.name} (${credential.computerId})`)
  io.out(`Expires:  ${credential.credentialExpiresAt ?? 'never'}`)
  const installation = thisInstallation()
  io.out(`Upgrades: ${'unavailable' in installation ? `not by itself, as ${installation.unavailable}` : `with ${installation.npmCli} into ${installation.prefix}`}`)
  if (await outdatedService(io) !== undefined) io.out(`Service:  ${OUTDATED_SERVICE}`)
  return EXIT_OK
}

async function service(args: string[], io: CliIO): Promise<number> {
  const [action, ...rest] = args
  parse(rest, {})
  if (action !== 'install' && action !== 'status' && action !== 'uninstall') {
    throw new UsageError(action === undefined ? 'service needs install, status or uninstall' : `unknown service command "${action}"`)
  }
  const host = io.serviceHost()
  const manager = serviceManager(host, io.paths)
  switch (action) {
    case 'install': {
      const credential = await readCredential(io.paths)
      if (credential === undefined) {
        io.err('This machine is not registered. Run "talgent-computer register" first.')
        return EXIT_NOT_REGISTERED
      }
      await manager.install(serviceSpec(host, io.paths))
      const { logs } = await manager.status()
      io.out(`Talgent Computer runs "${credential.name}" as a background service now and starts ${manager.startsAt}`)
      io.out(`Its output: ${logs}`)
      return EXIT_OK
    }
    case 'status': {
      const state = await manager.status()
      if (!state.installed) {
        io.out('No background service installed. Run "talgent-computer service install".')
        return EXIT_SERVICE_NOT_INSTALLED
      }
      const ended = state.lastExit === undefined ? 'not running' : `not running (last run exited ${state.lastExit}${reason(state.lastExit)})`
      io.out(`Service: ${state.running ? `running${state.pid === undefined ? '' : ` (pid ${state.pid})`}` : ended}`)
      io.out(`Defined: ${state.definition}`)
      if (!state.supervisesUpgrades) io.out(`Upgrade: ${OUTDATED_SERVICE}`)
      io.out(`Output:  ${state.logs}`)
      return EXIT_OK
    }
    case 'uninstall': {
      const removed = await manager.uninstall()
      io.out(removed ? 'Removed the background service. Its Work processes keep running; the next "talgent-computer run" picks them up.' : 'No background service was installed.')
      return EXIT_OK
    }
  }
}

function reason(code: number): string {
  const text = exitReason(code)
  return text === undefined ? '' : `: ${text}`
}

class UsageError extends Error {}

function parse<T extends ParseArgsOptionsConfig>(args: string[], options: T) {
  try {
    return parseArgs({ args, options, allowPositionals: false, strict: true as const })
  } catch (error) {
    throw new UsageError(error instanceof Error ? error.message : String(error))
  }
}

export function normalizeServer(input: string): string {
  let url: URL
  try {
    url = new URL(input)
  } catch {
    throw new UsageError(`invalid server "${input}"`)
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new UsageError(`server must be http(s): "${input}"`)
  return url.origin
}

function defaultIO(): CliIO {
  return {
    out: (line) => process.stdout.write(line + '\n'),
    err: (line) => process.stderr.write(line + '\n'),
    paths: resolvePaths(),
    serviceHost: currentServiceHost,
  }
}
