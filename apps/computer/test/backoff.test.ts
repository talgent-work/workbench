import assert from 'node:assert/strict'
import { test } from 'node:test'
import { backoffDelayMs, RECONNECT_BASE_ENV, reconnectBackoff } from '../src/backoff.ts'

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

test('a run backs off from 1s unless its environment names a shorter first window', () => {
  for (const value of [undefined, '', '0', '-5', '1.5', 'soon']) {
    const ms = reconnectBackoff(value === undefined ? {} : { [RECONNECT_BASE_ENV]: value })(0)
    assert.ok(ms >= 500 && ms <= 1_000, `${String(value)}: ${ms} outside [500, 1000]`)
  }
  const short = reconnectBackoff({ [RECONNECT_BASE_ENV]: '10' })
  assert.ok(short(0) >= 5 && short(0) <= 10)
  assert.ok(short(20) >= 15_000 && short(20) <= 30_000, 'the cap still holds')
})
