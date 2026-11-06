// Reconnect delays: exponential from 1s, capped at 30s, with jitter so many
// machines dropped together do not reconnect together. Each delay is drawn
// from the upper half of its window, keeping a floor while spreading retries.
export const BACKOFF_BASE_MS = 1_000
export const BACKOFF_MAX_MS = 30_000

export function backoffDelayMs(attempt: number, random: () => number = Math.random): number {
  const window = Math.min(BACKOFF_MAX_MS, BACKOFF_BASE_MS * 2 ** Math.max(0, attempt))
  return Math.round(window / 2 + random() * (window / 2))
}
