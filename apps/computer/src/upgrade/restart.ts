import { spawn } from 'node:child_process'
import { EXIT_ERROR, EXIT_OK, EXIT_RESTART, RUN_ENDED_FOR_GOOD } from '../exit-codes.ts'

// How a run restarts into the version installed now. A run whose starter
// starts it again after EXIT_RESTART, the background service or a
// supervising run, says so in SUPERVISED_ENV and just exits with it. A run
// a person started in a terminal must keep going there: it becomes the
// supervisor, starting "run" again as its child until the child exits with
// anything but EXIT_RESTART. A child that failed may have run a new version
// on probation that broke before it could watch its own probation: the
// supervisor then rolls that version back and starts the previous one. A
// background service starts a failed run again itself, and the new version
// counts its starts. Work processes never belong to a run, so
// neither way of restarting ends them.

export const SUPERVISED_ENV = 'TALGENT_COMPUTER_SUPERVISED'

export function supervised(env: NodeJS.ProcessEnv): boolean {
  return env[SUPERVISED_ENV] === '1'
}

export interface RunCommand {
  node: string
  args: string[]
  env: NodeJS.ProcessEnv
}

// Starts the run again and again while it exits with EXIT_RESTART, or with
// a failure after which failed answers true, having gone back to the
// previous version, and answers its last exit code. An interrupt typed at
// the terminal reaches the child, which stops; a termination sent to this
// process is passed on and ends supervising.
export async function superviseRuns(command: RunCommand, log: (message: string) => void, failed: (code: number) => Promise<boolean> = async () => false): Promise<number> {
  const ignoreInterrupt = () => {}
  process.on('SIGINT', ignoreInterrupt)
  try {
    for (;;) {
      const { code, terminated } = await runOnce(command)
      if (code === EXIT_RESTART) {
        log('restarting')
        continue
      }
      if (terminated || code === EXIT_OK || RUN_ENDED_FOR_GOOD.includes(code) || !(await failed(code))) return code
      log('restarting into the previous version')
    }
  } finally {
    process.off('SIGINT', ignoreInterrupt)
  }
}

function runOnce(command: RunCommand): Promise<{ code: number; terminated: boolean }> {
  return new Promise((resolve) => {
    const child = spawn(command.node, command.args, { stdio: 'inherit', env: { ...command.env, [SUPERVISED_ENV]: '1' }, windowsHide: true })
    let terminated = false
    const terminate = () => {
      terminated = true
      child.kill('SIGTERM')
    }
    process.on('SIGTERM', terminate)
    process.on('SIGHUP', terminate)
    let settled = false
    const settle = (code: number) => {
      if (settled) return
      settled = true
      process.off('SIGTERM', terminate)
      process.off('SIGHUP', terminate)
      resolve({ code, terminated })
    }
    child.once('error', () => settle(EXIT_ERROR))
    child.once('exit', (code) => settle(code ?? EXIT_ERROR))
  })
}
