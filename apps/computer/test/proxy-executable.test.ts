import assert from 'node:assert/strict'
import { mkdtemp, readdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, sep } from 'node:path'
import { after, before, describe, test } from 'node:test'
import { output, Pipe } from './support/execd-client.ts'
import { computerTarget } from './support/targets.ts'
import { Upstream } from './support/upstream.ts'

// The executable driven through its Gateway protocol, the black-box seam:
// Talgent Computer runs as its own process behind a stand-in Gateway, the
// platform delivers a Work's credentials over the tunnel, and a Work process
// reaches a stand-in model vendor and MCP endpoint only through the local
// proxy. TALGENT_COMPUTER_MAIN runs it against the build.

const KEY = 'sk-executable-model-key'
const WORK_CREDENTIAL = 'executable-work-credential'
const REPORT = '# Published from a Managed Work\n'

let target: Awaited<ReturnType<typeof computerTarget>>
let upstream: Upstream
let scripts: string

// A Work process that waits for a line on stdin, then calls the model and
// MCP routes as Claude Code would, publishing a Work file, and prints what
// it saw.
const CLIENT = `
const env = process.env
console.log('ENV ' + JSON.stringify(env))
for await (const _line of process.stdin) break
const model = await fetch(env.ANTHROPIC_BASE_URL + '/v1/messages', {
  method: 'POST', headers: { authorization: 'Bearer ' + env.ANTHROPIC_AUTH_TOKEN, 'content-type': 'application/json' }, body: '{}',
})
const publish = { jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: 'publish_artifact', arguments: { source_path: '/workspace/report.md' } } }
const mcp = await fetch(env.TALGENT_PROXY_URL + '/mcp', {
  method: 'POST', headers: { authorization: 'Bearer ' + env.TALGENT_PROXY_TOKEN, 'content-type': 'application/json' }, body: JSON.stringify(publish),
})
const link = JSON.parse(await mcp.text()).link
const content = await fetch(link)
console.log('STATUS ' + model.status + ' ' + mcp.status + ' ' + content.status)
console.log('CONTENT ' + await content.text())
`

// Talgent Computer's own files under root. The copies of Claude Code it
// runs Work processes on are left out: each is the bundled executable copied
// byte for byte before any credential arrives, and reading hundreds of
// megabytes of it would dominate the case.
async function files(root: string): Promise<string[]> {
  const copies = join(root, 'claude-code')
  const entries = await readdir(root, { withFileTypes: true, recursive: true })
  return entries
    .filter((entry) => entry.isFile() && entry.parentPath !== copies && !entry.parentPath.startsWith(copies + sep))
    .map((entry) => join(entry.parentPath, entry.name))
}

// One machine serves every case; starting it, which copies the bundled
// Claude Code, is the suite's setup rather than any case's.
describe('the executable behind its Gateway', () => {
  before(async () => {
    upstream = new Upstream()
    await upstream.listen()
    scripts = await mkdtemp(join(tmpdir(), 'proxy-client-'))
    await writeFile(join(scripts, 'client.mjs'), CLIENT)
    target = await computerTarget({ machineEnv: { ANTHROPIC_API_KEY: 'machine-key' }, modelProxy: true })
  })
  after(async () => {
    await target.close()
    await upstream.close()
    await rm(scripts, { recursive: true, force: true })
  })

  test('a Work process reaches the model with the injected key, also after a restart, and never holds the key', async () => {
    const { client } = target
    await writeFile(join(target.gateway.workRoot, 'report.md'), REPORT)
    // The MCP endpoint answers with a Storage content link the model fetches as it is.
    upstream.answer = (url, response) => {
      if (url === '/computer/v1/mcp') {
        response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ link: `${upstream.origin}/computer/v1/storage/content?token=signed` }))
        return true
      }
      if (!url.startsWith('/computer/v1/storage/content')) return false
      response.writeHead(200, { 'Content-Type': 'text/plain' }).end('stored bytes')
      return true
    }
    const credentials = {
      model: { base_url: `${upstream.origin}/anthropic`, key: KEY },
      gateway: { url: `${upstream.origin}/computer/v1/mcp`, work_id: 'work-1', work_credential: WORK_CREDENTIAL },
    }
    assert.equal((await client.call('PUT', '/credentials', credentials, '')).status, 204)

    const id = await client.create(target.cwd, `"${process.execPath}" "${join(scripts, 'client.mjs')}"`)
    const first = await client.attach(id, 0)
    assert.ok(first instanceof Pipe)
    const seen = await first.until((_frame, frames) => output(frames, 'stdout').includes('\n'))
    const env = JSON.parse(output(seen, 'stdout').split('\n')[0].slice('ENV '.length)) as Record<string, string>
    assert.equal(env.ANTHROPIC_API_KEY, undefined)
    assert.match(env.ANTHROPIC_BASE_URL, /^http:\/\/127\.0\.0\.1:\d+\/w\/work-1\/model$/)
    assert.equal(env.ANTHROPIC_AUTH_TOKEN, env.TALGENT_PROXY_TOKEN)
    for (const secret of [KEY, WORK_CREDENTIAL]) assert.ok(!JSON.stringify(env).includes(secret))

    // The process keeps its proxy URL and token across a restart.
    first.detach()
    await target.restart?.()
    const second = await client.attachWhenFree(id, Buffer.byteLength(output(seen, 'stdout')))
    second.stdin('go\n')
    const rest = await second.until((frame) => frame.type === 'exit')
    assert.match(output(rest, 'stdout'), /STATUS 200 200 200\nCONTENT stored bytes\n/)
    const content = upstream.seen.find((request) => request.url.startsWith('/computer/v1/storage/content'))
    assert.equal(content?.url, '/computer/v1/storage/content?token=signed')
    assert.equal(content?.headers['x-talgent-work-credential'], WORK_CREDENTIAL)
    assert.deepEqual(rest.at(-1), { type: 'exit', exit_code: 0 })

    const model = upstream.seen.find((request) => request.url === '/anthropic/v1/messages')
    assert.equal(model?.headers.authorization, `Bearer ${KEY}`)
    const mcp = upstream.seen.find((request) => request.url === '/computer/v1/mcp')
    assert.equal(mcp?.headers['x-talgent-work-credential'], WORK_CREDENTIAL)
    assert.equal(mcp?.headers.authorization, undefined)
    const published = JSON.parse(mcp?.body ?? '{}') as { params: { arguments: Record<string, string> } }
    assert.equal(published.params.arguments.source_content, Buffer.from(REPORT).toString('base64'), 'the publication carries the file bytes')

    // Only the credentials file holds them among Talgent Computer's files.
    for (const file of await files(target.dataDir)) {
      if (file.endsWith(join('credentials', 'work-1.json'))) continue
      const text = await readFile(file, 'utf8').catch(() => '')
      assert.ok(!text.includes(KEY) && !text.includes(WORK_CREDENTIAL), `${file} holds a secret`)
    }
    assert.equal((await client.call('DELETE', `/pty/${id}`)).status, 200)
  })

  test('the platform browses and reads a Work file through the tunnel', async () => {
    const { client, gateway } = target
    await writeFile(join(gateway.workRoot, 'notes.txt'), 'browsed bytes')
    const listing = await client.call('GET', '/files?path=')
    assert.equal(listing.status, 200)
    const entries = (listing.body as { entries: { name: string; kind: string; size: number }[] }).entries
    const notes = entries.find((entry) => entry.name === 'notes.txt')
    assert.equal(notes?.kind, 'file')
    assert.equal(notes?.size, 13)
    const read = await gateway.readFile('notes.txt')
    assert.deepEqual(read, { status: 0, data: Buffer.from('browsed bytes') })
    assert.equal((await gateway.readFile('../outside')).status, 404)
    assert.equal((await gateway.readFile('notes.txt', 'wrong')).status, 401)
  })
})
