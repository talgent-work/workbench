import { spawn } from 'node:child_process'
import { COMPUTER_PACKAGE, type Installation } from './installation.ts'

// Installs a release of Talgent Computer with npm. npm checks each
// package's integrity against the sha512 the registry publishes and refuses
// a download that does not match (EINTEGRITY); a refusal leaves the
// installed package as it was.

export interface ExecResult {
  code: number
  stdout: string
  stderr: string
}

// Runs an executable to its end; never rejects for a nonzero exit, and
// answers code -1 when it could not run or ran past timeoutMs.
export type Exec = (file: string, args: string[], options: { timeoutMs: number }) => Promise<ExecResult>

export interface Installer {
  // Installs this release in place of the installed one.
  install(version: string): Promise<void>
  // What the installed command answers to --version.
  installedVersion(): Promise<string>
}

export const INSTALL_TIMEOUT_MS = 10 * 60_000
const VERSION_TIMEOUT_MS = 30_000
// The end of npm's output kept for the log.
const KEPT_OUTPUT = 4096

export class NpmInstaller implements Installer {
  private readonly installation: Installation
  private readonly exec: Exec

  constructor(installation: Installation, exec: Exec = execFileResult) {
    this.installation = installation
    this.exec = exec
  }

  async install(version: string): Promise<void> {
    const { node, npmCli, prefix } = this.installation
    const result = await this.exec(node, [npmCli, 'install', '--global', '--prefix', prefix, '--no-audit', '--no-fund', `${COMPUTER_PACKAGE}@${version}`], { timeoutMs: INSTALL_TIMEOUT_MS })
    if (result.code !== 0) throw new Error(`npm install ${COMPUTER_PACKAGE}@${version} failed (${result.code === -1 ? 'did not finish' : `exit ${result.code}`}): ${npmFailure(result.stderr || result.stdout)}`)
  }

  async installedVersion(): Promise<string> {
    const result = await this.exec(this.installation.node, [this.installation.program, '--version'], { timeoutMs: VERSION_TIMEOUT_MS })
    if (result.code !== 0) throw new Error(`the installed talgent-computer does not start: ${lastLine(result.stderr || result.stdout)}`)
    return result.stdout.trim()
  }
}

// npm's error lines say what failed, such as "code EINTEGRITY"; the line
// pointing at npm's own log file does not.
function npmFailure(output: string): string {
  const errors = output.split(/\r?\n/)
    .map((line) => /^npm (?:error|ERR!) (.*)$/.exec(line.trim())?.[1]?.trim() ?? '')
    .filter((line) => line !== '' && !/complete log of this run|_logs[/\\]/.test(line))
  return errors.length === 0 ? lastLine(output) : errors.slice(0, 3).join('; ')
}

function lastLine(output: string): string {
  const lines = output.trim().split(/\r?\n/).filter((line) => line.trim() !== '')
  return lines.at(-1)?.trim() ?? 'no output'
}

// npm runs detached: in its own process group, and on Windows in its own
// hidden console, so an interrupt typed at the terminal (Ctrl+C reaches
// every process of the foreground group or of the console) stops the run
// but never cuts an install short halfway. Past its timeout npm and,
// outside Windows, what it started are killed.
export const execFileResult: Exec = (file, args, { timeoutMs }) =>
  new Promise((resolve) => {
    const child = spawn(file, args, { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true, detached: true })
    let stdout = ''
    let stderr = ''
    child.stdout.on('data', (chunk: Buffer) => { stdout = (stdout + chunk.toString()).slice(-KEPT_OUTPUT) })
    child.stderr.on('data', (chunk: Buffer) => { stderr = (stderr + chunk.toString()).slice(-KEPT_OUTPUT) })
    const timer = setTimeout(() => {
      try {
        if (process.platform !== 'win32' && child.pid !== undefined) process.kill(-child.pid, 'SIGKILL')
        else child.kill('SIGKILL')
      } catch {
        // It ended meanwhile.
      }
    }, timeoutMs)
    let settled = false
    const settle = (code: number) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      resolve({ code, stdout, stderr })
    }
    child.once('error', (error) => {
      stderr += error.message
      settle(-1)
    })
    child.once('close', (code) => settle(code ?? -1))
  })
