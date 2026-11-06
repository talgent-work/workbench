// A minimal Connect unary client over JSON: one POST per call, errors come
// back as {"code","message"} with a non-200 status.
export type Fetch = typeof fetch

export class ConnectError extends Error {
  readonly code: string
  constructor(code: string, message: string) {
    super(message === '' ? code : `${code}: ${message}`)
    this.code = code
  }
}

export async function callUnary(
  fetchImpl: Fetch,
  server: string,
  service: string,
  method: string,
  request: unknown,
  timeoutMs = 30_000,
): Promise<unknown> {
  const url = new URL(`/api/${service}/${method}`, server)
  const response = await fetchImpl(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' },
    body: JSON.stringify(request),
    signal: AbortSignal.timeout(timeoutMs),
  })
  const text = await response.text()
  let body: unknown = undefined
  if (text !== '') {
    try {
      body = JSON.parse(text)
    } catch {
      body = undefined
    }
  }
  if (!response.ok) {
    const record = (typeof body === 'object' && body !== null ? body : {}) as Record<string, unknown>
    const code = typeof record.code === 'string' ? record.code : codeFromStatus(response.status)
    const message = typeof record.message === 'string' ? record.message : ''
    throw new ConnectError(code, message)
  }
  if (typeof body !== 'object' || body === null) {
    throw new ConnectError('internal', `unexpected response from ${method}`)
  }
  return body
}

function codeFromStatus(status: number): string {
  switch (status) {
    case 400: return 'invalid_argument'
    case 401: return 'unauthenticated'
    case 403: return 'permission_denied'
    case 404: return 'not_found'
    case 429:
    case 502:
    case 503:
    case 504: return 'unavailable'
    default: return 'unknown'
  }
}
