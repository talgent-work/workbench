import { createHash } from 'node:crypto'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'

// A stand-in npm registry for the @talgent scope: it serves the packument
// of @talgent/computer and each release's tarball, with the sha512
// integrity npm checks every download against. A release may declare the
// integrity of other bytes than it serves, which npm must refuse.

export interface Release {
  manifest: Record<string, unknown>
  tarball: Buffer
  // The bytes whose integrity the packument declares; the tarball by default.
  declared?: Buffer
}

export class Registry {
  origin = ''
  readonly releases = new Map<string, Release>()
  private readonly http = createServer((request, response) => {
    const url = new URL(request.url ?? '/', 'http://registry')
    const tarball = /^\/tarballs\/(.+)\.tgz$/.exec(url.pathname)
    if (tarball !== null) {
      const release = this.releases.get(tarball[1])
      if (release === undefined) return void response.writeHead(404).end()
      return void response.writeHead(200, { 'Content-Type': 'application/octet-stream' }).end(release.tarball)
    }
    if (decodeURIComponent(url.pathname).toLowerCase() !== '/@talgent/computer') return void response.writeHead(404).end()
    const versions: Record<string, unknown> = {}
    for (const [version, release] of this.releases) {
      const declared = release.declared ?? release.tarball
      versions[version] = {
        ...release.manifest,
        version,
        dist: {
          tarball: `${this.origin}/tarballs/${version}.tgz`,
          integrity: `sha512-${createHash('sha512').update(declared).digest('base64')}`,
          shasum: createHash('sha1').update(declared).digest('hex'),
        },
      }
    }
    response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ name: '@talgent/computer', 'dist-tags': { latest: [...this.releases.keys()][0] }, versions }))
  })

  async listen(): Promise<void> {
    await new Promise<void>((resolve) => this.http.listen(0, '127.0.0.1', resolve))
    this.origin = `http://127.0.0.1:${(this.http.address() as AddressInfo).port}`
  }

  async close(): Promise<void> {
    this.http.closeAllConnections()
    await new Promise<void>((resolve) => this.http.close(() => resolve()))
  }
}
