import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Fetch } from '../src/connect-json.ts'
import { awaitRegistration, RegistrationExpiredError, startRegistration, type RegistrationDeps } from '../src/registration.ts'

type Reply = { status: number; body: unknown } | Error

// A stub fetch answering each call from a queue, recording what was asked.
function stub(replies: Reply[]) {
  const calls: { url: string; body: unknown }[] = []
  let clock = 0
  const fetchImpl: Fetch = async (input, init) => {
    calls.push({ url: String(input), body: JSON.parse(String(init?.body)) })
    const reply = replies.shift()
    if (reply === undefined) throw new Error('unexpected call')
    if (reply instanceof Error) throw reply
    return new Response(JSON.stringify(reply.body), { status: reply.status, headers: { 'Content-Type': 'application/json' } })
  }
  const deps: RegistrationDeps = {
    fetch: fetchImpl,
    sleep: async (ms) => { clock += ms },
    now: () => clock,
  }
  return { deps, calls }
}

const started = {
  deviceCode: 'dev-1',
  userCode: 'ABCD-EFGH',
  verificationUri: 'https://talgent.example/computer/activate',
  verificationUriComplete: 'https://talgent.example/computer/activate?code=ABCD-EFGH',
  expiresInSeconds: 60,
  intervalSeconds: 5,
}

test('start sends the machine name and reads the codes', async () => {
  const { deps, calls } = stub([{ status: 200, body: { ...started, expiresInSeconds: 600, intervalSeconds: 5 } }])
  const result = await startRegistration(deps, 'https://talgent.example', 'laptop')
  assert.equal(calls[0].url, 'https://talgent.example/api/talgent.computer.public.v1.RegistrationService/StartRegistration')
  assert.deepEqual(calls[0].body, { machineName: 'laptop' })
  assert.equal(result.userCode, 'ABCD-EFGH')
  assert.equal(result.expiresInSeconds, 600)
})

test('polls through pending and transient failures until registered', async () => {
  const { deps, calls } = stub([
    { status: 200, body: { status: 'REDEMPTION_STATUS_PENDING' } },
    new TypeError('fetch failed'),
    { status: 503, body: { code: 'unavailable', message: 'restarting' } },
    {
      status: 200,
      body: {
        status: 'REDEMPTION_STATUS_REGISTERED',
        computerId: 'c-1',
        name: 'laptop',
        credential: 'secret',
        credentialExpiresAtUnixMs: '1798761600000',
      },
    },
  ])
  const credential = await awaitRegistration(deps, 'https://talgent.example', started)
  assert.equal(calls.length, 4)
  assert.ok(calls.every((call) => (call.body as { deviceCode: string }).deviceCode === 'dev-1'))
  assert.deepEqual(credential, {
    server: 'https://talgent.example',
    computerId: 'c-1',
    name: 'laptop',
    credential: 'secret',
    credentialExpiresAt: '2027-01-01T00:00:00.000Z',
  })
})

test('not_found ends the wait as expired', async () => {
  const { deps } = stub([
    { status: 200, body: { status: 'REDEMPTION_STATUS_PENDING' } },
    { status: 404, body: { code: 'not_found', message: 'registration expired' } },
  ])
  await assert.rejects(awaitRegistration(deps, 'https://talgent.example', started), RegistrationExpiredError)
})

test('stops polling once the registration lifetime passes', async () => {
  const pending = { status: 200, body: { status: 'REDEMPTION_STATUS_PENDING' } }
  const { deps, calls } = stub(Array.from({ length: 20 }, () => pending))
  await assert.rejects(awaitRegistration(deps, 'https://talgent.example', started), RegistrationExpiredError)
  assert.equal(calls.length, 12)
})

test('other errors surface', async () => {
  const { deps } = stub([{ status: 400, body: { code: 'invalid_argument', message: 'bad device code' } }])
  await assert.rejects(awaitRegistration(deps, 'https://talgent.example', started), /invalid_argument: bad device code/)
})
