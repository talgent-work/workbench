import { deleteVariable } from './environment.ts'

// Where a Work's Claude Code sends its telemetry, as the platform names it in
// the Work's environment PUT: the platform's public OTLP endpoint and the run's
// telemetry token, which writes only that Work's telemetry. The Work's
// environment already chooses what is reported; this adds where it goes and how
// it authenticates.
export interface WorkTelemetry {
  endpoint: string
  token: string
}

export class InvalidTelemetryError extends Error {}

// Signal-specific settings the OTLP exporters prefer over the generic ones;
// the machine's own would send the Work's telemetry, and its token,
// elsewhere.
const SIGNAL_OVERRIDES = ['TRACES', 'LOGS', 'METRICS'].flatMap((signal) =>
  ['ENDPOINT', 'HEADERS', 'PROTOCOL'].map((setting) => `OTEL_EXPORTER_OTLP_${signal}_${setting}`),
)

// A token travels in an OTLP header list unencoded, so it holds none of its
// delimiters.
const TOKEN = /^[A-Za-z0-9._~-]{16,512}$/

export function parseTelemetry(value: unknown): WorkTelemetry | undefined {
  if (value === undefined) return undefined
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new InvalidTelemetryError('telemetry must be an object')
  const { endpoint, token } = value as Record<string, unknown>
  if (typeof endpoint !== 'string' || !httpsOrigin(endpoint)) throw new InvalidTelemetryError('telemetry endpoint must be an HTTPS URL')
  if (typeof token !== 'string' || !TOKEN.test(token)) throw new InvalidTelemetryError('invalid telemetry token')
  return { endpoint, token }
}

function httpsOrigin(value: string): boolean {
  let url: URL
  try {
    url = new URL(value)
  } catch {
    return false
  }
  return url.protocol === 'https:' && url.username === '' && url.password === '' && url.search === '' && url.hash === ''
}

// Points env's OTLP exporters at the platform with the Work's telemetry
// token and identity, replacing whatever the machine set for them.
export function applyTelemetry(env: NodeJS.ProcessEnv, workId: string, telemetry: WorkTelemetry, platform: NodeJS.Platform): void {
  for (const name of [...SIGNAL_OVERRIDES, 'OTEL_EXPORTER_OTLP_ENDPOINT', 'OTEL_EXPORTER_OTLP_HEADERS']) deleteVariable(env, name, platform)
  env.OTEL_EXPORTER_OTLP_ENDPOINT = telemetry.endpoint
  env.OTEL_EXPORTER_OTLP_HEADERS = `Authorization=Bearer ${telemetry.token},X-Talgent-Work-Id=${workId}`
}
