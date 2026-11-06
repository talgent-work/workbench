import { homedir } from 'node:os'
import { posix, resolve, win32 } from 'node:path'

// Where Talgent Computer keeps its files. XDG_CONFIG_HOME and XDG_DATA_HOME
// decide when set; otherwise Linux and macOS use the XDG defaults under the
// home directory and Windows its per-user application data folders: the
// roaming one for configuration, the local one for program data.
export interface Paths {
  configDir: string
  configFile: string
  credentialFile: string
  // Program data: Work roots and process state.
  dataDir: string
}

export function resolvePaths(
  env: NodeJS.ProcessEnv = process.env,
  home: string = homedir(),
  platform: NodeJS.Platform = process.platform,
): Paths {
  const path = platform === 'win32' ? win32 : posix
  const windows = platform === 'win32'
  const configHome = nonEmpty(env.XDG_CONFIG_HOME) ??
    (windows ? nonEmpty(env.APPDATA) ?? path.join(home, 'AppData', 'Roaming') : path.join(home, '.config'))
  const dataHome = nonEmpty(env.XDG_DATA_HOME) ??
    (windows ? nonEmpty(env.LOCALAPPDATA) ?? path.join(home, 'AppData', 'Local') : path.join(home, '.local', 'share'))
  const configDir = path.join(configHome, 'talgent-computer')
  return {
    configDir,
    configFile: path.join(configDir, 'config.json'),
    credentialFile: path.join(configDir, 'credential.json'),
    dataDir: path.join(dataHome, 'talgent-computer'),
  }
}

function nonEmpty(value: string | undefined): string | undefined {
  return value !== undefined && value !== '' ? value : undefined
}

// The program file this process runs as talgent-computer: what the person,
// the background service, a supervisor and every Work process's shim start.
export function thisProgram(): string {
  return resolve(process.argv[1] ?? '')
}
