import assert from 'node:assert/strict'
import { test } from 'node:test'
import { backoffDelayMs } from '../src/backoff.ts'

test('backoff doubles from 1s and caps at 30s', () => {
  const upper = [0, 1, 2, 3, 4, 5, 6, 10].map((attempt) => backoffDelayMs(attempt, () => 1))
  assert.deepEqual(upper, [1_000, 2_000, 4_000, 8_000, 16_000, 30_000, 30_000, 30_000])
})

test('backoff jitter stays within the upper half of each window', () => {
  assert.equal(backoffDelayMs(0, () => 0), 500)
  assert.equal(backoffDelayMs(9, () => 0), 15_000)
  for (let i = 0; i < 100; i++) {
    const ms = backoffDelayMs(3)
    assert.ok(ms >= 4_000 && ms <= 8_000, `${ms} outside [4000, 8000]`)
  }
})
