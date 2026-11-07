// Reconnect delays: exponential from 1s, capped at 30s, with jitter so many
// machines dropped together do not reconnect together. Each delay is drawn
// from the upper half of its window, keeping a floor while spreading retries.
export const BACKOFF_BASE_MS = 1_000
export const BACKOFF_MAX_MS = 30_000

// Starts the backoff from a shorter first window. Only the tests that drop
// a running machine's connection on purpose set it, so each reconnection
// costs them milliseconds instead of seconds; a run without it, or with
// anything but a positive integer, backs off from BACKOFF_BASE_MS.
export const RECONNECT_BASE_ENV = 'TALGENT_COMPUTER_RECONNECT_BASE_MS'

export function backoffDelayMs(attempt: number, random: () => number = Math.random, baseMs = BACKOFF_BASE_MS): number {
  const window = Math.min(BACKOFF_MAX_MS, baseMs * 2 ** Math.max(0, attempt))
  return Math.round(window / 2 + random() * (window / 2))
}

// The reconnect delays of a run in env.
export function reconnectBackoff(env: NodeJS.ProcessEnv): (attempt: number) => number {
  const configured = Number(env[RECONNECT_BASE_ENV])
  const baseMs = Number.isSafeInteger(configured) && configured > 0 ? configured : BACKOFF_BASE_MS
  return (attempt) => backoffDelayMs(attempt, Math.random, baseMs)
}
