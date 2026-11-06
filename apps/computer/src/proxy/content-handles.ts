import { randomBytes } from 'node:crypto'

// Local content links: each rewritten Storage content link becomes an
// unguessable handle bound to the Work whose MCP response carried it and to
// the link's query. The content route serves only handles issued for its
// Work, so no other local process borrows a Work's identity through it.

// Longer than the platform's 15-minute content links, which then refuse.
export const CONTENT_HANDLE_TTL_MS = 20 * 60_000
const MAX_HANDLES = 10_000

interface Issued {
  workId: string
  query: string
  expires: number
}

export interface ContentHandlesOptions {
  ttlMs?: number
  // Handles kept at most; issuing more forgets the oldest.
  max?: number
  now?: () => number
}

export class ContentHandles {
  private readonly ttlMs: number
  private readonly max: number
  private readonly now: () => number
  // In the order issued, which with one lifetime is the order they expire.
  private readonly issued = new Map<string, Issued>()

  constructor(options: ContentHandlesOptions = {}) {
    this.ttlMs = options.ttlMs ?? CONTENT_HANDLE_TTL_MS
    this.max = options.max ?? MAX_HANDLES
    this.now = options.now ?? Date.now
  }

  // A new handle for the Work's content link of query.
  issue(workId: string, query: string): string {
    const now = this.now()
    for (const [handle, issued] of this.issued) {
      if (issued.expires > now && this.issued.size < this.max) break
      this.issued.delete(handle)
    }
    const handle = randomBytes(24).toString('base64url')
    this.issued.set(handle, { workId, query, expires: now + this.ttlMs })
    return handle
  }

  // The query of a live handle issued for workId.
  query(workId: string, handle: string): string | undefined {
    const issued = this.issued.get(handle)
    if (issued === undefined || issued.workId !== workId || issued.expires <= this.now()) return undefined
    return issued.query
  }
}
