import { accessSync, constants } from 'node:fs'
import { posix } from 'node:path'

// Whether this machine can provide Claude Code's sandbox, which a Work may
// ask to run in. Talgent Computer reports it on every connection
// (X-Talgent-Claude-Code-Sandbox), and the platform rejects a sandboxed start
// on a machine that cannot provide it. Claude Code's own sandbox needs:
//   - macOS: Seatbelt's sandbox-exec, part of the system;
//   - Linux: bubblewrap (bwrap) and socat on PATH;
//   - native Windows: nothing it can use, so never.
// The report is a promise checked again at launch: a sandboxed Work's Claude
// Code refuses to start without a working sandbox, so a machine whose check
// passes but whose sandbox fails runs no Work unsandboxed.

export interface SandboxHost {
  platform: NodeJS.Platform
  // The PATH a Work process would search.
  path: string | undefined
  // Whether a file exists and is executable.
  executable?: (file: string) => boolean
}

const isExecutable = (file: string): boolean => {
  try {
    accessSync(file, constants.X_OK)
    return true
  } catch {
    return false
  }
}

export function claudeCodeSandboxAvailable(host: SandboxHost): boolean {
  const executable = host.executable ?? isExecutable
  switch (host.platform) {
    case 'darwin':
      return executable('/usr/bin/sandbox-exec')
    case 'linux': {
      const dirs = (host.path ?? '').split(posix.delimiter).filter((dir) => dir !== '')
      const found = (name: string) => dirs.some((dir) => executable(posix.join(dir, name)))
      return found('bwrap') && found('socat')
    }
    default:
      return false
  }
}

// The connection header's value for this machine.
export function sandboxHeader(host: SandboxHost): string {
  return claudeCodeSandboxAvailable(host) ? 'available' : 'unavailable'
}
