import { execFile } from 'node:child_process'
import { readFile } from 'node:fs/promises'
import { SUPERVISED_ENV } from '../upgrade/restart.ts'

// Talgent Computer as the person's own background service: it starts at
// login and starts again after it exits, except after a run that ended for
// good (RUN_ENDED_FOR_GOOD). It runs plain "talgent-computer run" as the
// person, so their Claude Code login, ~/.claude and git credentials apply;
// every other setting comes from the configuration file.

// What the service runs: program and args, with the environment the run
// needs to find the same files and tools as the person's shell.
export interface ServiceSpec {
  program: string
  args: string[]
  env: Record<string, string>
  workingDir: string
  // Where the platform writes the run's output, when it does not keep it.
  logFile: string
}

export interface ServiceStatus {
  installed: boolean
  running: boolean
  pid?: number
  // The last run's exit code, when the platform knows it.
  lastExit?: number
  // Whether the installed service tells its run it is supervised, so the
  // run restarts into an upgrade by exiting; one installed by a Talgent
  // Computer before self-upgrade does not (serviceSpec in command.ts).
  supervisesUpgrades: boolean
  // Where the platform registered the service.
  definition: string
  // How to read the run's output.
  logs: string
}

export interface ServiceManager {
  // When the platform starts the service, said to the person who installs it.
  readonly startsAt: string
  // Installs or replaces the service and starts it now.
  install(spec: ServiceSpec): Promise<void>
  status(): Promise<ServiceStatus>
  // Stops and removes the service; false when it was not installed.
  uninstall(): Promise<boolean>
}

export interface CommandResult {
  code: number
  stdout: string
  stderr: string
}

// Runs a platform tool; never rejects for a nonzero exit.
export type RunCommand = (command: string, args: string[]) => Promise<CommandResult>

export const runCommand: RunCommand = (command, args) =>
  new Promise((resolve, reject) => {
    execFile(command, args, { windowsHide: true, encoding: 'utf8' }, (error, stdout, stderr) => {
      if (error !== null && typeof error.code !== 'number') {
        reject(new Error(`${command} could not run: ${error.message}`))
        return
      }
      resolve({ code: error === null ? 0 : Number(error.code), stdout, stderr })
    })
  })

// A platform tool that must succeed.
export async function mustRun(run: RunCommand, command: string, args: string[]): Promise<string> {
  const result = await run(command, args)
  if (result.code !== 0) {
    const reason = (result.stderr.trim() || result.stdout.trim() || `exit code ${result.code}`)
    throw new Error(`${command} ${args.join(' ')} failed: ${reason}`)
  }
  return result.stdout
}

export const SERVICE_NAME = 'talgent-computer'

// Whether the definition file the service runs from sets SUPERVISED_ENV.
export async function definesSupervised(file: string): Promise<boolean> {
  try {
    return (await readFile(file, 'utf8')).includes(SUPERVISED_ENV)
  } catch {
    return false
  }
}
