// What a test may expect of the machine it runs on. File modes are POSIX:
// on Windows a mode only toggles the read-only attribute, and the per-user
// profile folders keep the files private instead.
export const posixModes = process.platform !== 'win32'

// Why a test that relies on POSIX process or shell behavior is skipped on
// Windows, where WindowsHost realizes Work processes (windows-host.test.ts).
export function posixOnly(reason: string): string | false {
  return process.platform === 'win32' ? reason : false
}
