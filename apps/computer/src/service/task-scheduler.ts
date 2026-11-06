import { mkdir, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { RUN_ENDED_FOR_GOOD } from '../exit-codes.ts'
import { readRun } from '../run-record.ts'
import { xml } from './launchd.ts'
import { definesSupervised, mustRun, type RunCommand, type ServiceManager, type ServiceSpec, type ServiceStatus } from './manager.ts'

export const TASK_NAME = 'Talgent Computer'
const WRAPPER_FILE = 'talgent-computer-service.cmd'
const TASK_FILE = 'talgent-computer-task.xml'

// Windows: a scheduled task that starts at the person's logon, in their
// session and without elevation. Task Scheduler restarts nothing after an
// exit, so a small batch file runs the command again unless the run ended
// for good. A headless console host keeps any window from opening.
export class LogonTaskService implements ServiceManager {
  readonly startsAt = 'at logon.'
  private readonly run: RunCommand
  private readonly user: string
  private readonly serviceDir: string
  private readonly logFile: string
  private readonly runFile: string

  constructor(run: RunCommand, user: string, serviceDir: string, logFile: string, runFile: string) {
    this.runFile = runFile
    this.run = run
    this.user = user
    this.serviceDir = serviceDir
    this.logFile = logFile
  }

  async install(spec: ServiceSpec): Promise<void> {
    await mkdir(this.serviceDir, { recursive: true })
    await mkdir(join(spec.logFile, '..'), { recursive: true })
    const wrapper = join(this.serviceDir, WRAPPER_FILE)
    const definition = join(this.serviceDir, TASK_FILE)
    await writeFile(wrapper, wrapperScript(spec))
    // Task Scheduler reads a task definition as UTF-16.
    await writeFile(definition, Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from(logonTask(spec, this.user, wrapper), 'utf16le')]))
    await this.end()
    await mustRun(this.run, 'schtasks', ['/Create', '/TN', TASK_NAME, '/XML', definition, '/F'])
    await mustRun(this.run, 'schtasks', ['/Run', '/TN', TASK_NAME])
  }

  async status(): Promise<ServiceStatus> {
    // Get-ScheduledTask names states the same in every display language.
    const script = `$t = Get-ScheduledTask -TaskName '${TASK_NAME}' -ErrorAction SilentlyContinue; ` +
      `if ($null -eq $t) { exit 3 }; $i = $t | Get-ScheduledTaskInfo; "$($t.State)|$($i.LastTaskResult)"`
    const queried = await this.run('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script])
    const [state, lastResult] = queried.stdout.trim().split('|')
    const installed = queried.code === 0 && state !== undefined && state !== ''
    return {
      installed,
      running: installed && state === 'Running',
      lastExit: installed && lastResult !== undefined && /^\d+$/.test(lastResult) && state !== 'Running' ? Number(lastResult) : undefined,
      supervisesUpgrades: installed && await definesSupervised(join(this.serviceDir, WRAPPER_FILE)),
      definition: `Task Scheduler: ${TASK_NAME}`,
      logs: this.logFile,
    }
  }

  async uninstall(): Promise<boolean> {
    const { installed } = await this.status()
    if (installed) {
      await this.end()
      await mustRun(this.run, 'schtasks', ['/Delete', '/TN', TASK_NAME, '/F'])
    }
    await rm(join(this.serviceDir, WRAPPER_FILE), { force: true })
    await rm(join(this.serviceDir, TASK_FILE), { force: true })
    return installed
  }

  // Ending the task ends its console host only: the batch file and the run
  // it started go on. The batch file goes first, so it cannot start the run
  // again, then the run — each alone, as Work processes descend from the run.
  private async end(): Promise<void> {
    await this.run('schtasks', ['/End', '/TN', TASK_NAME])
    const record = await readRun(this.runFile)
    if (record === undefined) return
    for (const [pid, image] of [[record.ppid, 'cmd.exe'], [record.pid, 'node.exe']] as const) {
      // A recorded pid another process took since is left alone.
      const listed = await this.run('tasklist', ['/FI', `PID eq ${pid}`, '/FO', 'CSV', '/NH'])
      if (listed.stdout.toLowerCase().includes(`"${image}"`)) await this.run('taskkill', ['/F', '/PID', String(pid)])
    }
  }
}

export function wrapperScript(spec: ServiceSpec): string {
  // Within a batch file % starts a variable; doubled, it is a literal %.
  const literal = (value: string) => value.replace(/%/g, '%%')
  const command = [spec.program, ...spec.args].map((part) => `"${literal(part)}"`).join(' ')
  return [
    '@echo off',
    'chcp 65001 >nul',
    ...Object.entries(spec.env).map(([name, value]) => `set "${name}=${literal(value)}"`),
    `cd /d "${literal(spec.workingDir)}"`,
    ':run',
    `${command} >> "${literal(spec.logFile)}" 2>&1`,
    'set code=%errorlevel%',
    'if %code%==0 exit /b 0',
    ...RUN_ENDED_FOR_GOOD.map((code) => `if %code%==${code} exit /b 0`),
    // timeout needs console input, which a headless console has not.
    'ping -n 11 127.0.0.1 >nul',
    'goto run',
    '',
  ].join('\r\n')
}

export function logonTask(spec: ServiceSpec, user: string, wrapper: string): string {
  return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Talgent Computer: keeps this machine connected to Talgent.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>${xml(user)}</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>${xml(user)}</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>conhost.exe</Command>
      <Arguments>${xml(`--headless cmd.exe /d /s /c ""${wrapper}""`)}</Arguments>
      <WorkingDirectory>${xml(spec.workingDir)}</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`
}
