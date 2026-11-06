import { main } from './cli.ts'
import { currentHost } from './execd/host.ts'
import { runShim, SHIM_ARG } from './execd/shim.ts'

// Talgent Computer runs each Work process through itself as a detached shim.
if (process.argv[2] === SHIM_ARG) runShim(process.argv[3], Number(process.argv[4]), currentHost())
else process.exitCode = await main(process.argv.slice(2))
