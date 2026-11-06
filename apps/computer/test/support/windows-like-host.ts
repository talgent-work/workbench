import { createHash } from 'node:crypto'
import { join } from 'node:path'
import { WindowsHost } from '../../src/execd/windows-host.ts'

// Windows' realization of Work processes run on Linux and macOS: a Unix
// socket stands in for the named pipe and the shim's process group for its
// tree; the stdin, liveness and kill logic is Windows'. The socket lives
// under /tmp, as a socket path must stay short.
export function windowsLikeHost(): WindowsHost {
  return new WindowsHost({
    pipePath: (stateDir, run) => join('/tmp', `tc-${createHash('sha256').update(`${stateDir}\0${run}`).digest('hex').slice(0, 16)}.sock`),
    killTree: async (pid) => {
      try {
        process.kill(-pid, 'SIGKILL')
      } catch {
        // Already gone.
      }
    },
    shell: () => 'bash',
  })
}
