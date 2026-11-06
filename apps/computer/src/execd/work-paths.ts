import nativePath, { posix, type PlatformPath } from 'node:path'

// A Work root is laid out as a Cloud Work's /workspace: the
// platform names a process's working directory under this prefix and the
// machine maps it into the Work root. Commands are run as the platform sent
// them: they reach the Work root through TALGENT_WORK_ROOT or their working
// directory, never through a rewrite of their text.
export const CLOUD_WORKSPACE = '/workspace'

// The path relative to the Work root that a Cloud /workspace path names, ""
// being the Work root itself; undefined for any other path. The platform
// names Cloud paths POSIX-style on every machine.
export function workspaceRelative(path: string): string | undefined {
  if (!posix.isAbsolute(path)) return undefined
  const normalized = posix.normalize(path)
  if (normalized === CLOUD_WORKSPACE) return ''
  return normalized.startsWith(CLOUD_WORKSPACE + '/') ? normalized.slice(CLOUD_WORKSPACE.length + 1) : undefined
}

// The machine directory a process cwd names: /workspace and below map into
// the Work root, any other absolute path is the machine's own, and a relative
// cwd is taken from the Work root. path is the machine's path flavour.
export function mapCwd(cwd: string, root: string, path: PlatformPath = nativePath): string {
  if (cwd === '') return root
  const inRoot = workspaceRelative(cwd)
  if (inRoot !== undefined) return confine(root, inRoot, path) ?? root
  if (path.isAbsolute(cwd)) return path.normalize(cwd)
  return confine(root, cwd, path) ?? root
}

// Whether target is dir or below it, on any drive and with either separator.
export function within(dir: string, target: string, path: PlatformPath = nativePath): boolean {
  const back = path.relative(dir, target)
  return back === '' || (back !== '..' && !back.startsWith('..' + path.sep) && !path.isAbsolute(back))
}

function confine(root: string, rel: string, path: PlatformPath): string | undefined {
  const target = path.join(root, rel)
  return within(root, target, path) ? target : undefined
}
