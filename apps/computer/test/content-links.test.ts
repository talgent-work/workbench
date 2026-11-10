import assert from 'node:assert/strict'
import { mkdtemp, rm } from 'node:fs/promises'
import { request as httpRequest } from 'node:http'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { ContentHandles } from '../src/proxy/content-handles.ts'
import type { WorkCredentials } from '../src/proxy/credentials.ts'
import { LocalProxy } from '../src/proxy/server.ts'
import { send, Upstream } from './support/upstream.ts'

const IDENTITY_TOKEN = 'agent-identity-token'

// Storage content links: MCP responses
// get links to the platform's content endpoint rewritten to local handles of
// the proxy's content route, which forwards them with the Work's identity.

const TOKEN = 'run-token'
const WORK_CREDENTIAL = 'content-work-credential'

let dir: string
let upstream: Upstream
let proxy: LocalProxy
let credentials: WorkCredentials | undefined
let now: number

beforeEach(async () => {
  dir = await mkdtemp(join(tmpdir(), 'content-links-'))
  now = 1_000_000
  upstream = new Upstream()
  await upstream.listen()
  credentials = { gateway: { url: `${upstream.origin}/computer/v1/mcp`, workId: 'w-1', identityToken: IDENTITY_TOKEN, workCredential: WORK_CREDENTIAL, spaceToken: 'space-1' } }
  proxy = new LocalProxy({
    portFile: join(dir, 'proxy.json'),
    access: {
      tokenValid: async (_workId, token) => token === TOKEN,
      credentials: async () => credentials,
      modelProxy: async () => false,
      workRoot: (workId) => join(dir, workId),
    },
    log: () => {},
    contentHandles: new ContentHandles({ ttlMs: 60_000, max: 3, now: () => now }),
  })
  await proxy.start()
})
afterEach(async () => {
  await proxy.close()
  await upstream.close()
  await rm(dir, { recursive: true, force: true })
})

const platformLink = (query: string) => `${upstream.origin}/computer/v1/storage/content?${query}`
const LOCAL = /http:\/\/127\.0\.0\.1:\d+\/w\/w-1\/content\/[A-Za-z0-9_-]{32}/g
const localLinks = (text: string): string[] => text.match(LOCAL) ?? []
const mcp = () => send(`${proxy.origin()}/w/w-1/mcp`, { method: 'POST', headers: { authorization: `Bearer ${TOKEN}` }, body: '{}' })

// Answers every MCP call with body, as JSON.
function answerMcp(body: string): void {
  upstream.answer = (url, response) => {
    if (!url.startsWith('/computer/v1/mcp')) return false
    response.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8', 'Content-Length': Buffer.byteLength(body) }).end(body)
    return true
  }
}

// The upstream query a local link reaches the stand-in content endpoint
// with, which answers it like any other request.
async function upstreamQuery(link: string): Promise<string> {
  const before = upstream.seen.length
  assert.equal((await send(link)).status, 200)
  assert.equal(upstream.seen.length, before + 1)
  return upstream.last().url.replace('/computer/v1/storage/content?', '')
}

test('content links in a JSON MCP response become local handles; others stay', async () => {
  const foreign = 'https://elsewhere.example/computer/v1/storage/content?t=3'
  const other = `${upstream.origin}/computer/v1/other?t=4`
  const text = JSON.stringify({ result: { content: [{ type: 'text', text: `a ${platformLink('t=1&x=2')} b ${platformLink('t=2')}. c ${foreign} d ${other} é` }] } })
  answerMcp(text)
  const answer = await mcp()
  const links = localLinks(answer.body)
  assert.equal(links.length, 2)
  assert.equal(answer.body, text.replace(platformLink('t=1&x=2'), links[0]).replace(platformLink('t=2'), links[1]), 'a full stop after a link stays outside it')
  assert.equal(answer.headers['content-length'], String(Buffer.byteLength(answer.body)))
  assert.equal(await upstreamQuery(links[0]), 't=1&x=2')
  assert.equal(await upstreamQuery(links[1]), 't=2')

  // A JSON-escaped & is part of the query.
  answerMcp(`{"text":"${platformLink('t=5\\u0026x=6')}"}`)
  const [escaped] = localLinks((await mcp()).body)
  assert.equal(await upstreamQuery(escaped), 't=5&x=6')

  // A response longer than the bound, or of another type, passes unchanged.
  const long = JSON.stringify({ text: platformLink('t=1'), padding: 'x'.repeat(1024 * 1024) })
  const plain = `see ${platformLink('t=1')}`
  for (const [type, body] of [['application/json', long], ['text/plain', plain]]) {
    upstream.answer = (_url, response) => {
      response.writeHead(200, { 'Content-Type': type }).end(body)
      return true
    }
    assert.equal((await mcp()).body, body, type)
  }
})

test('content links in a streamed MCP response are rewritten line by line as they arrive', async () => {
  let finish: () => void = () => {}
  const link = platformLink('t=9')
  upstream.answer = (_url, response) => {
    response.writeHead(200, { 'Content-Type': 'text/event-stream' })
    // The link is split across writes; the line still arrives whole.
    response.write(`event: message\ndata: {"text":"${link.slice(0, 20)}`)
    setTimeout(() => response.write(`${link.slice(20)}"}\n\n`), 20)
    finish = () => response.end(`data: ${link}\n\n`)
    return true
  }
  const { first, rest } = await new Promise<{ first: string; rest: Promise<string> }>((resolve, reject) => {
    const outgoing = httpRequest(`${proxy.origin()}/w/w-1/mcp`, { method: 'POST', headers: { authorization: `Bearer ${TOKEN}` } }, (response) => {
      let text = ''
      let resolved = false
      const rest = new Promise<string>((done) => response.on('end', () => done(text)))
      response.on('data', (chunk: Buffer) => {
        text += chunk.toString()
        if (!resolved && text.includes('}\n\n')) {
          resolved = true
          resolve({ first: text, rest })
          finish()
        }
      })
    })
    outgoing.on('error', reject)
    outgoing.end('{}')
  })
  const [firstLink] = localLinks(first)
  assert.equal(first, `event: message\ndata: {"text":"${firstLink}"}\n\n`, 'the first event arrives before the stream ends')
  const all = await rest
  const [, secondLink] = localLinks(all)
  assert.equal(all, `${first}data: ${secondLink}\n\n`)
  assert.notEqual(firstLink, secondLink, 'each link gets its own handle')
  upstream.answer = undefined
  assert.equal(await upstreamQuery(secondLink), 't=9')
})

// A local link to the platform's content of query, issued through an MCP
// response.
async function issue(query: string): Promise<string> {
  answerMcp(JSON.stringify({ text: platformLink(query) }))
  const [link] = localLinks((await mcp()).body)
  return link
}

test('the content route forwards with the Work identity, needs no run token and only reads', async () => {
  const link = await issue('token=signed&x=1')
  upstream.answer = (url, response) => {
    if (!url.startsWith('/computer/v1/storage/content')) return false
    response.writeHead(200, { 'Content-Type': 'application/pdf' }).end('pdf bytes')
    return true
  }
  const answer = await send(`${link}?ignored=1`, { headers: { authorization: 'Bearer anything', 'x-talgent-work-id': 'forged' } })
  assert.equal(answer.status, 200)
  assert.equal(answer.body, 'pdf bytes')
  const seen = upstream.last()
  assert.equal(seen.url, '/computer/v1/storage/content?token=signed&x=1')
  assert.equal(seen.headers['x-talgent-work-id'], 'w-1')
  assert.equal(seen.headers['x-talgent-work-credential'], WORK_CREDENTIAL)
  assert.equal(seen.headers['x-talgent-space-token'], 'space-1')
  assert.equal(seen.headers.authorization, undefined)
  assert.equal((await send(link, { method: 'HEAD' })).status, 200)
  const before = upstream.seen.length
  assert.equal((await send(link, { method: 'POST', body: '{}' })).status, 405)
  credentials = undefined
  const undelivered = await send(link)
  assert.equal(undelivered.status, 503)
  assert.equal(JSON.parse(undelivered.body).code, 'CREDENTIALS_NOT_DELIVERED')
  assert.equal(upstream.seen.length, before, 'refused requests reach nothing')
})

test('the content route serves only live handles issued for its Work', async () => {
  const link = await issue('t=1')
  const handle = link.slice(link.lastIndexOf('/') + 1)
  const before = upstream.seen.length
  const refused = [
    `${proxy.origin()}/w/w-2/content/${handle}`,
    `${proxy.origin()}/w/w-1/content/${'A'.repeat(32)}`,
    `${proxy.origin()}/w/w-1/content?t=1`,
    `${proxy.origin()}/w/w-1/content/${handle}/x`,
    `${proxy.origin()}/w/a.b/content/${handle}`,
  ]
  for (const url of refused) assert.equal((await send(url)).status, 404, url)
  now += 60_000
  assert.equal((await send(link)).status, 404, 'an expired handle')
  // Past the bound, the oldest handles are forgotten.
  const kept = [await issue('t=2'), await issue('t=3'), await issue('t=4'), await issue('t=5')]
  assert.equal((await send(kept[0])).status, 404, 'the oldest handle')
  assert.equal(upstream.seen.length, before + 4, 'refused requests reach nothing; issuing goes through /mcp')
  assert.equal(await upstreamQuery(kept[3]), 't=5')
})
