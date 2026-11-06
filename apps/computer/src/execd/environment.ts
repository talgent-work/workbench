// Composing a process's environment the way the machine reads it: Windows
// treats variable names case-insensitively, so a later layer's Path replaces
// an earlier PATH instead of standing beside it.

export function mergeEnvironment(platform: NodeJS.Platform, ...layers: NodeJS.ProcessEnv[]): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {}
  for (const layer of layers) {
    for (const [name, value] of Object.entries(layer)) {
      if (value === undefined) continue
      if (platform === 'win32') for (const key of keysNamed(env, name, platform)) delete env[key]
      env[name] = value
    }
  }
  return env
}

// Puts dir first on the environment's PATH.
export function prependPath(env: NodeJS.ProcessEnv, dir: string, platform: NodeJS.Platform): void {
  const keys = keysNamed(env, 'PATH', platform)
  const key = keys.at(-1) ?? 'PATH'
  const current = env[key] ?? ''
  for (const name of keys) delete env[name]
  env[key] = current === '' ? dir : `${dir}${platform === 'win32' ? ';' : ':'}${current}`
}

export function deleteVariable(env: NodeJS.ProcessEnv, name: string, platform: NodeJS.Platform): void {
  for (const key of keysNamed(env, name, platform)) delete env[key]
}

function keysNamed(env: NodeJS.ProcessEnv, name: string, platform: NodeJS.Platform): string[] {
  if (platform !== 'win32') return name in env ? [name] : []
  const upper = name.toUpperCase()
  return Object.keys(env).filter((key) => key.toUpperCase() === upper)
}
