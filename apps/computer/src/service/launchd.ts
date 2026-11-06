import { mkdir, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { RUN_ENDED_FOR_GOOD } from '../exit-codes.ts'
import { definesSupervised, mustRun, type RunCommand, type ServiceManager, type ServiceSpec, type ServiceStatus } from './manager.ts'

export const LAUNCHD_LABEL = 'work.talgent.computer'

// macOS: a LaunchAgent in the person's login session.
export class LaunchAgentService implements ServiceManager {
  readonly startsAt = 'at login.'
  private readonly run: RunCommand
  private readonly plistFile: string
  private readonly target: string
  private readonly domain: string
  private readonly logFile: string

  constructor(run: RunCommand, home: string, uid: number, logFile: string) {
    this.run = run
    this.logFile = logFile
    this.plistFile = join(home, 'Library', 'LaunchAgents', `${LAUNCHD_LABEL}.plist`)
    this.domain = `gui/${uid}`
    this.target = `${this.domain}/${LAUNCHD_LABEL}`
  }

  async install(spec: ServiceSpec): Promise<void> {
    await mkdir(join(this.plistFile, '..'), { recursive: true })
    await mkdir(join(spec.logFile, '..'), { recursive: true })
    await writeFile(this.plistFile, launchAgentPlist(spec), { mode: 0o644 })
    // Replacing a loaded agent takes unloading it first.
    await this.run('launchctl', ['bootout', this.target])
    await mustRun(this.run, 'launchctl', ['bootstrap', this.domain, this.plistFile])
  }

  async status(): Promise<ServiceStatus> {
    const printed = await this.run('launchctl', ['print', this.target])
    const installed = printed.code === 0
    const field = (name: string) => new RegExp(`^\\s*${name} = (.+)$`, 'm').exec(printed.stdout)?.[1]?.trim()
    const pid = Number(field('pid') ?? 0)
    const lastExit = field('last exit code')
    return {
      installed,
      running: installed && field('state') === 'running' && pid > 0,
      pid: pid > 0 ? pid : undefined,
      lastExit: lastExit !== undefined && /^-?\d+$/.test(lastExit) ? Number(lastExit) : undefined,
      supervisesUpgrades: installed && await definesSupervised(this.plistFile),
      definition: this.plistFile,
      logs: this.logFile,
    }
  }

  async uninstall(): Promise<boolean> {
    const { installed } = await this.status()
    if (installed) await mustRun(this.run, 'launchctl', ['bootout', this.target])
    await rm(this.plistFile, { force: true })
    return installed
  }
}

// launchd restarts a job only by its exit status, so a small sh stands
// between: a run that ended for good exits 0, which launchd leaves stopped,
// and the TERM launchd sends on unload reaches the run.
const WRAPPER = [
  'trap \'kill -TERM "$child" 2>/dev/null\' TERM INT',
  '"$0" "$@" & child=$!',
  'wait "$child"; code=$?',
  // A trapped signal interrupts the first wait; the second has the run's status.
  'if [ "$code" -gt 128 ]; then wait "$child"; code=$?; fi',
  `case "$code" in ${RUN_ENDED_FOR_GOOD.join('|')}) exit 0;; esac`,
  'exit "$code"',
].join('\n')

export function launchAgentPlist(spec: ServiceSpec): string {
  const strings = (values: string[]) => values.map((value) => `    <string>${xml(value)}</string>`).join('\n')
  const environment = Object.entries(spec.env)
    .map(([name, value]) => `    <key>${xml(name)}</key>\n    <string>${xml(value)}</string>`).join('\n')
  return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${LAUNCHD_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
${strings(['/bin/sh', '-c', WRAPPER, spec.program, ...spec.args])}
  </array>
  <key>EnvironmentVariables</key>
  <dict>
${environment}
  </dict>
  <key>WorkingDirectory</key>
  <string>${xml(spec.workingDir)}</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>StandardOutPath</key>
  <string>${xml(spec.logFile)}</string>
  <key>StandardErrorPath</key>
  <string>${xml(spec.logFile)}</string>
</dict>
</plist>
`
}

export function xml(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}
