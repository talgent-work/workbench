import { userInfo } from 'node:os'
import { posix, win32 } from 'node:path'
import { RUN_RECORD_PATH } from '../run-record.ts'
import { thisProgram, type Paths } from '../paths.ts'
import { SUPERVISED_ENV } from '../upgrade/restart.ts'
import { LaunchAgentService } from './launchd.ts'
import { runCommand, type RunCommand, type ServiceManager, type ServiceSpec } from './manager.ts'
import { SystemdUserService } from './systemd.ts'
import { LogonTaskService } from './task-scheduler.ts'

export interface ServiceHost {
  platform: NodeJS.Platform
  env: NodeJS.ProcessEnv
  // The node that runs talgent-computer, and its program file.
  node: string
  program: string
  run: RunCommand
  // The person's own account, whatever HOME the installing shell has.
  account: { home: string; uid: number; username: string }
}

export function currentServiceHost(): ServiceHost {
  const account = userInfo()
  return {
    platform: process.platform,
    env: process.env,
    node: process.execPath,
    program: thisProgram(),
    run: runCommand,
    account: { home: account.homedir, uid: account.uid, username: account.username },
  }
}

export function serviceManager(host: ServiceHost, paths: Paths): ServiceManager {
  const logFile = serviceLogFile(host, paths)
  switch (host.platform) {
    case 'linux': return new SystemdUserService(host.run, host.account.home)
    case 'darwin': return new LaunchAgentService(host.run, host.account.home, host.account.uid, logFile)
    case 'win32': {
      const user = host.env.USERDOMAIN ? `${host.env.USERDOMAIN}\\${host.account.username}` : host.account.username
      return new LogonTaskService(host.run, user, win32.join(paths.dataDir, 'service'), logFile, win32.join(paths.dataDir, ...RUN_RECORD_PATH))
    }
    default: throw new Error(`a background service is not supported on ${host.platform}; keep "talgent-computer run" running yourself`)
  }
}

// The service runs plain "talgent-computer run" with this installation's
// node and program. It finds tools on the installing shell's PATH and uses
// the configuration and data this installation uses, named explicitly so
// the service's own login environment cannot point elsewhere. The service
// starts a run again after it exits to restart into an upgrade, so the run
// is told it is supervised.
export function serviceSpec(host: ServiceHost, paths: Paths): ServiceSpec {
  const path = pathsOf(host)
  const env: Record<string, string> = {
    PATH: host.env.PATH ?? '',
    XDG_CONFIG_HOME: path.dirname(paths.configDir),
    XDG_DATA_HOME: path.dirname(paths.dataDir),
    [SUPERVISED_ENV]: '1',
  }
  return {
    program: host.node,
    args: [host.program, 'run'],
    env,
    workingDir: host.account.home,
    logFile: serviceLogFile(host, paths),
  }
}

// The path rules of the service's platform, as paths.ts uses them.
function pathsOf(host: ServiceHost) {
  return host.platform === 'win32' ? win32 : posix
}

function serviceLogFile(host: ServiceHost, paths: Paths): string {
  return pathsOf(host).join(paths.dataDir, 'logs', 'service.log')
}
