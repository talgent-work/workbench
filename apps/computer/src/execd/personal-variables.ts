import { mergeEnvironment } from './environment.ts'

// A run's Personal Variables: the person's own environment variables, which
// the platform sends with the creation of a Work's Harness process only, never
// with a preparation process or the Work's environment. They take the place of
// any variable of the same name in that process's environment, the machine's,
// the Work's and Talgent Computer's own alike, except the TALGENT_ variables,
// which stay Talgent Computer's. Talgent Computer keeps them in memory only,
// never on disk or in a log.
export type PersonalVariables = Readonly<Record<string, string>>

export class InvalidPersonalVariablesError extends Error {}

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/
// Talgent Computer's own names, in any case: Windows compares names without
// case, and the platform refuses such a Personal Variable in any case too.
const RESERVED = /^talgent_/i

// Reads the personal_variables of a process creation. Errors name neither a
// variable nor its value. A reserved name is left out.
export function parsePersonalVariables(value: unknown): PersonalVariables {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new InvalidPersonalVariablesError('personal_variables must be an object')
  }
  const variables: Record<string, string> = {}
  for (const [name, variable] of Object.entries(value)) {
    if (!NAME.test(name) || typeof variable !== 'string' || variable.includes('\0')) {
      throw new InvalidPersonalVariablesError('personal_variables holds an invalid variable')
    }
    if (!RESERVED.test(name)) variables[name] = variable
  }
  return variables
}

// Layers the variables over env, last: each replaces env's variable of the
// same name, as the machine compares names, except a reserved one.
export function layerPersonalVariables(env: NodeJS.ProcessEnv, variables: PersonalVariables, platform: NodeJS.Platform): NodeJS.ProcessEnv {
  const allowed = Object.fromEntries(Object.entries(variables).filter(([name]) => !RESERVED.test(name)))
  return mergeEnvironment(platform, env, allowed)
}
