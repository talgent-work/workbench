import { runShim } from '../../src/execd/shim.ts'
import { WindowsHost } from '../../src/execd/windows-host.ts'
import { windowsLikeHost } from './windows-like-host.ts'

// The shim of the Windows processes suite: argv is the state directory and the
// run. It loads only the shim, not the whole program, so a case on a Windows
// runner pays for the processes it checks and not for starting the CLI.
runShim(process.argv[2], Number(process.argv[3]), process.platform === 'win32' ? new WindowsHost() : windowsLikeHost())
