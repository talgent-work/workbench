import { accessSync, constants, existsSync, realpathSync } from 'node:fs'
import { posix, win32 } from 'node:path'

// Where npm installed this Talgent Computer, so that it upgrades itself
// with the npm next to the node that runs it, into the same prefix.

export const COMPUTER_PACKAGE = '@talgent-work/computer'

export interface Installation {
  // The node that runs Talgent Computer, and npm's own program next to it.
  node: string
  npmCli: string
  // The global prefix npm installed this package under.
  prefix: string
  // The command the person and the background service run.
  program: string
}

export interface InstallationProbe {
  // The package root: the directory holding this package's package.json.
  packageRoot: string
  node: string
  program: string
  platform?: NodeJS.Platform
  exists?: (path: string) => boolean
  writable?: (path: string) => boolean
}

// Finds the npm installation this package runs from, or answers why it
// cannot upgrade itself: it runs from a checkout, no npm is beside node, or
// its user cannot write the prefix (one installed with sudo, say), where
// every install would fail with EACCES.
// A global install lives at <prefix>/lib/node_modules/@talgent-work/computer, on
// Windows at <prefix>\node_modules\@talgent-work\computer.
export function findInstallation(probe: InstallationProbe): Installation | { unavailable: string } {
  const platform = probe.platform ?? process.platform
  const exists = probe.exists ?? existsSync
  const writable = probe.writable ?? canWrite
  const path = platform === 'win32' ? win32 : posix
  const root = path.resolve(probe.packageRoot)
  const scope = path.dirname(root)
  const nodeModules = path.dirname(scope)
  if (path.basename(root) !== 'computer' || path.basename(scope) !== '@talgent-work' || path.basename(nodeModules) !== 'node_modules') {
    return { unavailable: `it runs from ${root}, not from an "npm install -g ${COMPUTER_PACKAGE}"` }
  }
  let prefix = path.dirname(nodeModules)
  if (platform !== 'win32') {
    if (path.basename(prefix) !== 'lib') return { unavailable: `${root} is not in a global npm prefix` }
    prefix = path.dirname(prefix)
  }
  const nodeDir = path.dirname(probe.node)
  const npmCli = [
    platform === 'win32' ? path.join(nodeDir, 'node_modules', 'npm', 'bin', 'npm-cli.js') : path.join(nodeDir, '..', 'lib', 'node_modules', 'npm', 'bin', 'npm-cli.js'),
    // npm upgraded with "npm install -g npm" into the same prefix.
    path.join(nodeModules, 'npm', 'bin', 'npm-cli.js'),
  ].map((candidate) => path.normalize(candidate)).find(exists)
  if (npmCli === undefined) return { unavailable: `no npm is installed next to ${probe.node}` }
  // npm replaces the package under node_modules and its command beside it.
  const unwritable = [nodeModules, platform === 'win32' ? prefix : path.join(prefix, 'bin')].find((dir) => !writable(dir))
  if (unwritable !== undefined) return { unavailable: `this user cannot write ${unwritable}, where npm installs it` }
  return { node: probe.node, npmCli, prefix, program: probe.program }
}

// The node running now, through any symbolic links, as npm sits beside the
// real executable.
export function runningNode(): string {
  try {
    return realpathSync(process.execPath)
  } catch {
    return process.execPath
  }
}

function canWrite(dir: string): boolean {
  try {
    accessSync(dir, constants.W_OK)
    return true
  } catch {
    return false
  }
}
