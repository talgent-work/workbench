import { execFile } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { chmod, constants, copyFile, mkdir, readdir, readFile, realpath, rename, rm } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { isObject } from './json.ts'
import { isErrno } from './store.ts'

// The Claude Code Work processes use: the exact version Talgent Computer's
// package depends on, which equals the Claude Code the platform release pins
// for its Cloud image. npm installs it with Talgent Computer.
//
// Work processes never run it from inside the installed package: npm must
// be able to replace the package while Works run, and Windows refuses to
// replace an executable that runs. At the start of each run Talgent Computer
// copies the bundled executable to <copies>/<version>/ in its own data
// directory and puts that directory first on every process's PATH, where it
// answers as `claude`; a new bundled version applies from each Work's next
// run. A copy no running process uses is removed.
//
// The executable is the native package npm installs for this platform as an
// optional dependency of @anthropic-ai/claude-code. Its own install script
// only moves that executable into the package's bin/, so the copy does not
// need the script to have run, whatever npm's script policy.

export const CLAUDE_CODE_PACKAGE = '@anthropic-ai/claude-code'

export interface BundledClaudeCode {
  // The directory whose `claude` is the copy Work processes run.
  binDir: string
  // What the copy answers to --version.
  version: string
}

export class ClaudeCodeMissingError extends Error {}

// Runs an executable with arguments and answers its standard output.
export type Run = (file: string, args: string[]) => Promise<string>

const runFile: Run = (file, args) =>
  new Promise((resolve, reject) => {
    execFile(file, args, { timeout: 30_000, windowsHide: true }, (error, stdout) => {
      if (error) reject(error)
      else resolve(String(stdout))
    })
  })

// Talgent Computer's package root: src/ in a checkout, dist/ when built, both
// one level below it.
export const PACKAGE_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')

// A version names a directory under the copies.
const VERSION = /^[0-9A-Za-z][0-9A-Za-z.+-]*$/

export interface PrepareOptions {
  // Where the per-version copies live, in Talgent Computer's data directory.
  copiesDir: string
  packageRoot?: string
  platform?: NodeJS.Platform
  arch?: string
  // Whether this Linux runs musl rather than glibc.
  musl?: boolean
  run?: Run
}

// Finds the bundled Claude Code as Node resolves a dependency, from the
// package's own node_modules up through its ancestors', makes its version's
// copy unless one runs already, and checks that the copy runs.
export async function prepareClaudeCode(options: PrepareOptions): Promise<BundledClaudeCode> {
  const { copiesDir, packageRoot = PACKAGE_ROOT, platform = process.platform, arch = process.arch, musl = isMusl(), run = runFile } = options
  const bundled = await findBundled(packageRoot)
  if (!VERSION.test(bundled.version)) throw new ClaudeCodeMissingError(`the bundled Claude Code has no usable version (${JSON.stringify(bundled.version)}); reinstall Talgent Computer`)
  const binDir = join(copiesDir, bundled.version)
  const name = platform === 'win32' ? 'claude.exe' : 'claude'
  const copy = join(binDir, name)
  const existing = await answer(run, copy)
  if (existing !== undefined) return { binDir, version: parseClaudeVersion(existing) || bundled.version }
  const source = await executableOf(bundled.dir, platform, arch, musl)
  await mkdir(binDir, { recursive: true, mode: 0o700 })
  const tmp = join(binDir, `.${name}.${randomBytes(6).toString('hex')}`)
  try {
    // A clone where the file system makes one, else a copy.
    await copyFile(source, tmp, constants.COPYFILE_FICLONE)
    if (platform !== 'win32') await chmod(tmp, 0o755)
    const copied = await answer(run, tmp)
    if (copied === undefined) throw new ClaudeCodeMissingError(`the bundled Claude Code ${bundled.version} does not run on this machine; reinstall Talgent Computer`)
    try {
      await rename(tmp, copy)
    } catch (error) {
      // Another run of Talgent Computer made the copy meanwhile, which
      // Windows may already run.
      if (await answer(run, copy) === undefined) throw error
    }
    return { binDir, version: parseClaudeVersion(copied) || bundled.version }
  } finally {
    await rm(tmp, { force: true })
  }
}

// Removes every copy but the directories in keep: the current one and those
// running processes use. A copy Windows still runs
// cannot be removed and stays until a later removal.
export async function removeUnusedCopies(copiesDir: string, keep: ReadonlySet<string>, log: (message: string) => void): Promise<void> {
  let entries: string[]
  try {
    entries = await readdir(copiesDir)
  } catch (error) {
    if (isErrno(error, 'ENOENT')) return
    throw error
  }
  for (const entry of entries) {
    if (keep.has(join(copiesDir, entry))) continue
    try {
      await rm(join(copiesDir, entry), { recursive: true, force: true })
    } catch (error) {
      log(`cannot remove the unused Claude Code ${join(copiesDir, entry)} yet: ${error instanceof Error ? error.message : String(error)}`)
    }
  }
}

// Extracts the semantic version from `claude --version`, which prints e.g.
// "2.0.14 (Claude Code)". Anything unrecognised yields the empty string.
export function parseClaudeVersion(output: string): string {
  const match = /\b(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)\b/.exec(output)
  return match?.[1] ?? ''
}

async function findBundled(packageRoot: string): Promise<{ dir: string; version: string }> {
  for (let dir = packageRoot; ; dir = dirname(dir)) {
    const pkg = join(dir, 'node_modules', ...CLAUDE_CODE_PACKAGE.split('/'))
    const manifest = await readJson(join(pkg, 'package.json'))
    if (manifest !== undefined && typeof manifest.version === 'string') return { dir: pkg, version: manifest.version }
    if (dirname(dir) === dir) throw new ClaudeCodeMissingError(`${CLAUDE_CODE_PACKAGE} is not installed with Talgent Computer; reinstall it`)
  }
}

// The platform's native executable, found from the package's real
// directory as Node resolves its optional dependency.
async function executableOf(pkg: string, platform: NodeJS.Platform, arch: string, musl: boolean): Promise<string> {
  const native = `${CLAUDE_CODE_PACKAGE}-${platform}-${arch}${platform === 'linux' && musl ? '-musl' : ''}`
  const file = platform === 'win32' ? 'claude.exe' : 'claude'
  for (let dir = dirname(dirname(await realpath(pkg))); ; dir = dirname(dir)) {
    const base = dir.endsWith('node_modules') ? dir : join(dir, 'node_modules')
    const candidate = join(base, ...native.split('/'))
    if (await readJson(join(candidate, 'package.json')) !== undefined) return join(candidate, file)
    if (dirname(dir) === dir) {
      throw new ClaudeCodeMissingError(`${native}, the Claude Code executable for this machine, is not installed with Talgent Computer; reinstall Talgent Computer without --omit=optional`)
    }
  }
}

// What an executable answers to --version, or undefined when it does not run.
async function answer(run: Run, file: string): Promise<string | undefined> {
  try {
    return await run(file, ['--version'])
  } catch {
    return undefined
  }
}

function isMusl(): boolean {
  if (process.platform !== 'linux') return false
  const report = process.report.getReport() as { header?: { glibcVersionRuntime?: string } }
  return report.header?.glibcVersionRuntime === undefined
}

async function readJson(path: string): Promise<Record<string, unknown> | undefined> {
  let parsed: unknown
  try {
    parsed = JSON.parse(await readFile(path, 'utf8'))
  } catch {
    return undefined
  }
  return isObject(parsed) ? parsed : undefined
}
