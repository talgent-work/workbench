import { runShim } from '../../src/execd/shim.ts'
import { windowsLikeHost } from './windows-like-host.ts'

// The shim of windowsLikeHost: argv is the state directory and the run.
runShim(process.argv[2], Number(process.argv[3]), windowsLikeHost())
