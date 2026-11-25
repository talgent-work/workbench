import { rm } from 'node:fs/promises'

// What a test may expect of the machine it runs on. File modes are POSIX:
// on Windows a mode only toggles the read-only attribute, and the per-user
// profile folders keep the files private instead.
export const posixModes = process.platform !== 'win32'

// Why a test that relies on POSIX process or shell behavior is skipped on
// Windows, where WindowsHost realizes Work processes (windows-host.test.ts).
export function posixOnly(reason: string): string | false {
  return process.platform === 'win32' ? reason : false
}

// Rules that run real Work processes but hold alike on every platform run on
// Linux and macOS. On Windows each process start costs a Git Bash and its end a
// taskkill, which left these cases to the runner's load; Windows' own
// realization of Work processes is checked there for real, in
// windows-host.test.ts.
export const portableProcessRules = posixOnly('a rule that holds on every platform; windows-host.test.ts checks Windows\' realization')

// Removes a test's scratch directory. On Windows an ended process tree holds
// the directories it ran in for a moment after it is recorded as exited, so
// removal retries a bounded few times.
export function removeScratch(dir: string): Promise<void> {
  return rm(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
}
