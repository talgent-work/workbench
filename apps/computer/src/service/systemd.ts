import { mkdir, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { RUN_ENDED_FOR_GOOD } from '../exit-codes.ts'
import { definesSupervised, mustRun, SERVICE_NAME, type RunCommand, type ServiceManager, type ServiceSpec, type ServiceStatus } from './manager.ts'

const UNIT = `${SERVICE_NAME}.service`

// Linux: a systemd user unit. It starts with the person's user manager, at
// login, or at boot once "loginctl enable-linger" keeps the manager running.
export class SystemdUserService implements ServiceManager {
  readonly startsAt = 'at login. To start it at boot and keep it running after you log out, run "loginctl enable-linger".'
  private readonly run: RunCommand
  private readonly unitFile: string

  constructor(run: RunCommand, home: string) {
    this.run = run
    // The user manager reads ~/.config/systemd/user whatever XDG_CONFIG_HOME
    // the installing shell has.
    this.unitFile = join(home, '.config', 'systemd', 'user', UNIT)
  }

  async install(spec: ServiceSpec): Promise<void> {
    await mkdir(join(this.unitFile, '..'), { recursive: true })
    await writeFile(this.unitFile, systemdUnit(spec), { mode: 0o644 })
    await this.systemctl('daemon-reload')
    await this.systemctl('enable', UNIT)
    await this.systemctl('restart', UNIT)
  }

  async status(): Promise<ServiceStatus> {
    const shown = await this.run('systemctl', ['--user', 'show', UNIT, '--property=LoadState,ActiveState,MainPID,ExecMainStatus'])
    const fields = new Map(shown.stdout.split('\n').map((line) => {
      const at = line.indexOf('=')
      return [line.slice(0, at), line.slice(at + 1).trim()] as const
    }))
    const installed = shown.code === 0 && fields.get('LoadState') === 'loaded'
    const pid = Number(fields.get('MainPID') ?? 0)
    const lastExit = fields.get('ExecMainStatus')
    return {
      installed,
      running: installed && fields.get('ActiveState') === 'active' && pid > 0,
      pid: pid > 0 ? pid : undefined,
      lastExit: installed && lastExit !== undefined && lastExit !== '' ? Number(lastExit) : undefined,
      supervisesUpgrades: installed && await definesSupervised(this.unitFile),
      definition: this.unitFile,
      logs: `journalctl --user -u ${UNIT}`,
    }
  }

  async uninstall(): Promise<boolean> {
    const { installed } = await this.status()
    if (installed) await this.systemctl('disable', '--now', UNIT)
    await rm(this.unitFile, { force: true })
    await this.systemctl('daemon-reload')
    return installed
  }

  private systemctl(...args: string[]): Promise<string> {
    return mustRun(this.run, 'systemctl', ['--user', ...args])
  }
}

export function systemdUnit(spec: ServiceSpec): string {
  const environment = Object.entries(spec.env).map(([name, value]) => `Environment=${quote(`${name}=${value}`)}`)
  return [
    '[Unit]',
    'Description=Talgent Computer',
    '',
    '[Service]',
    `ExecStart=${[spec.program, ...spec.args].map(quote).join(' ')}`,
    // A path setting takes the rest of the line, unquoted.
    `WorkingDirectory=${spec.workingDir.replace(/%/g, '%%')}`,
    ...environment,
    'Restart=on-failure',
    'RestartSec=10',
    `RestartPreventExitStatus=${RUN_ENDED_FOR_GOOD.join(' ')}`,
    // Work processes outlive the run, as they do when a person stops it;
    // the next run picks them up.
    'KillMode=process',
    '',
    '[Install]',
    'WantedBy=default.target',
    '',
  ].join('\n')
}

// A systemd quoted word: specifiers (%) and variables ($) taken literally.
function quote(value: string): string {
  const escaped = value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/%/g, '%%').replace(/\$/g, '$$$$')
  return `"${escaped}"`
}
