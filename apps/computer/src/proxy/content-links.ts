import { Transform, pipeline, type TransformCallback } from 'node:stream'
import { relayAsIs, type ResponseRelay } from './forward.ts'
import { readHead } from './request-body.ts'

// Storage content links: the platform Connector answers a Managed Work's file
// link tools with links to <origin>/computer/v1/storage/content?<token>,
// which also need the Work's identity headers a model's curl cannot add. The
// local proxy rewrites them, in its MCP responses, to local links of its own
// content route, which adds the headers.

export const CONTENT_PATH = '/computer/v1/storage/content'
// A JSON response read whole to rewrite; a longer one passes unchanged.
const MAX_JSON_BYTES = 1024 * 1024
// An SSE line held while it has no end; a longer one passes unchanged.
const MAX_LINE_BYTES = 1024 * 1024

// The platform's content endpoint for a Work whose MCP endpoint is gatewayUrl.
export function contentBase(gatewayUrl: string): string {
  return new URL(gatewayUrl).origin + CONTENT_PATH
}

// The characters of a content link's query: URL-unreserved ones, % escapes,
// = & +, and & as a JSON body may escape it.
const QUERY = String.raw`(?:[A-Za-z0-9._~%=&+-]|\\u0026)*`

// A relay of MCP responses that replaces every link to the platform's
// content endpoint with the local link localLink gives for its query.
// Encoded responses, and types other than JSON and server-sent events, pass
// as they are.
export function rewritingContentLinks(gatewayUrl: string, localLink: (query: string) => string): ResponseRelay {
  const links = new RegExp(escapeRegExp(contentBase(gatewayUrl) + '?') + `(${QUERY})`, 'g')
  const rewrite = (text: string) => text.replace(links, (_link, query: string) => {
    // A sentence's full stop after a link is not the link's.
    const trailing = /\.*$/.exec(query)?.[0] ?? ''
    return localLink(query.slice(0, query.length - trailing.length).replaceAll('\\u0026', '&')) + trailing
  })
  return (answer, status, headers, response) => {
    const encoding = answer.headers['content-encoding']
    if (encoding !== undefined && encoding !== 'identity') return relayAsIs(answer, status, headers, response)
    const type = (answer.headers['content-type'] ?? '').toLowerCase()
    if (type.startsWith('text/event-stream')) {
      delete headers['content-length']
      response.writeHead(status, headers)
      response.flushHeaders()
      pipeline(answer, new LineRewriter(rewrite), response, (error) => {
        if (error) response.destroy()
      })
      return
    }
    if (!/^application\/([a-z.+-]*\+)?json\b/.test(type)) return relayAsIs(answer, status, headers, response)
    void readHead(answer, MAX_JSON_BYTES, Infinity).then(
      ({ chunks, rest }) => {
        if (rest === undefined) {
          const body = Buffer.from(rewrite(Buffer.concat(chunks).toString('utf8')))
          headers['content-length'] = String(body.length)
          response.writeHead(status, headers).end(body)
          return
        }
        response.writeHead(status, headers)
        for (const chunk of chunks) response.write(chunk)
        pipeline(rest, response, (error) => {
          if (error) response.destroy()
        })
      },
      () => response.destroy(),
    )
  }
}

// Rewrites a stream line by line, passing each complete line on at once.
class LineRewriter extends Transform {
  private readonly rewrite: (line: string) => string
  private pending = ''

  constructor(rewrite: (line: string) => string) {
    super()
    this.rewrite = rewrite
  }

  override _transform(chunk: Buffer, _encoding: BufferEncoding, done: TransformCallback): void {
    this.pending += chunk.toString('latin1')
    const end = this.pending.lastIndexOf('\n')
    if (end >= 0) {
      this.push(this.convert(this.pending.slice(0, end + 1)))
      this.pending = this.pending.slice(end + 1)
    }
    if (this.pending.length > MAX_LINE_BYTES) {
      this.push(Buffer.from(this.pending, 'latin1'))
      this.pending = ''
    }
    done()
  }

  override _flush(done: TransformCallback): void {
    if (this.pending !== '') this.push(this.convert(this.pending))
    done()
  }

  // Lines are held as latin1 so a chunk may split a character; the links
  // themselves are ASCII.
  private convert(text: string): Buffer {
    return Buffer.from(this.rewrite(text), 'latin1')
  }
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}
