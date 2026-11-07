import { callUnary, ConnectError, type Fetch } from './connect-json.ts'
import type { Credential } from './store.ts'

const SERVICE = 'computer.public.v1.RegistrationService'

export interface StartedRegistration {
  deviceCode: string
  userCode: string
  verificationUri: string
  verificationUriComplete: string
  expiresInSeconds: number
  intervalSeconds: number
}

export class RegistrationExpiredError extends Error {
  constructor() {
    super('the registration expired or was already used')
  }
}

export interface RegistrationDeps {
  fetch: Fetch
  sleep: (ms: number) => Promise<void>
  now: () => number
}

export async function startRegistration(deps: RegistrationDeps, server: string, machineName: string): Promise<StartedRegistration> {
  const body = (await callUnary(deps.fetch, server, SERVICE, 'StartRegistration', { machineName })) as Record<string, unknown>
  const deviceCode = str(body.deviceCode)
  const userCode = str(body.userCode)
  if (deviceCode === '' || userCode === '') throw new ConnectError('internal', 'StartRegistration returned no code')
  return {
    deviceCode,
    userCode,
    verificationUri: str(body.verificationUri),
    verificationUriComplete: str(body.verificationUriComplete) || str(body.verificationUri),
    expiresInSeconds: num(body.expiresInSeconds) || 600,
    intervalSeconds: Math.max(1, num(body.intervalSeconds) || 5),
  }
}

// Polls until the person approves the registration, returning the new
// Computer and its credential. Brief outages while waiting are retried; an
// expired, unknown or already redeemed registration ends the wait.
export async function awaitRegistration(
  deps: RegistrationDeps,
  server: string,
  started: StartedRegistration,
): Promise<Credential> {
  const deadline = deps.now() + started.expiresInSeconds * 1000
  const intervalMs = started.intervalSeconds * 1000
  for (;;) {
    await deps.sleep(intervalMs)
    if (deps.now() > deadline) throw new RegistrationExpiredError()
    let body: Record<string, unknown>
    try {
      body = (await callUnary(deps.fetch, server, SERVICE, 'RedeemRegistration', { deviceCode: started.deviceCode })) as Record<string, unknown>
    } catch (error) {
      if (error instanceof ConnectError && error.code === 'not_found') throw new RegistrationExpiredError()
      if (isTransient(error)) continue
      throw error
    }
    if (body.status !== 'REDEMPTION_STATUS_REGISTERED') continue
    const computerId = str(body.computerId)
    const credential = str(body.credential)
    if (computerId === '' || credential === '') throw new ConnectError('internal', 'RedeemRegistration returned no credential')
    const expiresAtMs = num(body.credentialExpiresAtUnixMs)
    return {
      server,
      computerId,
      name: str(body.name),
      credential,
      credentialExpiresAt: expiresAtMs > 0 ? new Date(expiresAtMs).toISOString() : null,
    }
  }
}

function isTransient(error: unknown): boolean {
  if (error instanceof ConnectError) return error.code === 'unavailable' || error.code === 'deadline_exceeded'
  // fetch rejects with TypeError on network failure and with a DOMException
  // on our own timeout.
  return error instanceof TypeError || (error instanceof Error && (error.name === 'TimeoutError' || error.name === 'AbortError'))
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

// int32 arrives as a JSON number, int64 as a JSON string.
function num(value: unknown): number {
  const n = typeof value === 'string' ? Number(value) : typeof value === 'number' ? value : NaN
  return Number.isFinite(n) ? n : 0
}
