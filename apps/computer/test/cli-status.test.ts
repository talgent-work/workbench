import assert from 'node:assert/strict'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { main, type CliIO } from '../src/cli.ts'
import { resolvePaths } from '../src/paths.ts'
import { serviceSpec, type ServiceHost } from '../src/service/command.ts'
import { systemdUnit } from '../src/service/systemd.ts'
import { writeCredential } from '../src/store.ts'
import { SUPERVISED_ENV } from '../src/upgrade/restart.ts'

let dir: string
beforeEach(async () => { dir = await mkdtemp(join(tmpdir(), 'cli-status-')) })
afterEach(() => rm(dir, { recursive: true, force: true }))

// A Linux machine whose systemd user manager has the service loaded from
// the unit file the test writes.
async function machine(unit: (host: ServiceHost) => string | undefined): Promise<{ io: CliIO; out: string[] }> {
  const paths = resolvePaths({ XDG_CONFIG_HOME: join(dir, 'config'), XDG_DATA_HOME: join(dir, 'data') }, dir, 'linux')
  await writeCredential(paths, { server: 'https://talgent.example', computerId: 'c1', name: 'desk', credential: 'secret', credentialExpiresAt: null }, { force: false })
  const host: ServiceHost = {
    platform: 'linux',
    env: { PATH: '/usr/bin' },
    node: '/usr/bin/node',
    program: '/pkg/talgent-computer.mjs',
    run: async (_command, args) => args[1] === 'show' ? { code: 0, stdout: 'LoadState=loaded\nActiveState=active\nMainPID=42\nExecMainStatus=0\n', stderr: '' } : { code: 0, stdout: '', stderr: '' },
    account: { home: dir, uid: 501, username: 'a' },
  }
  const written = unit(host)
  if (written !== undefined) {
    await mkdir(join(dir, '.config', 'systemd', 'user'), { recursive: true })
    await writeFile(join(dir, '.config', 'systemd', 'user', 'talgent-computer.service'), written)
  }
  const out: string[] = []
  return { io: { out: (line) => out.push(line), err: (line) => out.push(line), paths, serviceHost: () => host }, out }
}

test('a background service installed before self-upgrade is told to be installed again', async () => {
  const before = await machine((host) => {
    const spec = serviceSpec(host, resolvePaths({}, dir, 'linux'))
    const env = { ...spec.env }
    delete env[SUPERVISED_ENV]
    return systemdUnit({ ...spec, env })
  })
  assert.equal(await main(['status'], before.io), 0)
  assert.ok(before.out.some((line) => /^Service: .*run "talgent-computer service install" again/.test(line)), before.out.join('\n'))
  before.out.length = 0
  assert.equal(await main(['service', 'status'], before.io), 0)
  assert.ok(before.out.some((line) => /^Upgrade: .*service install" again/.test(line)), before.out.join('\n'))
})

test('a current background service, or none, says nothing about it', async () => {
  const current = await machine((host) => systemdUnit(serviceSpec(host, resolvePaths({}, dir, 'linux'))))
  assert.equal(await main(['status'], current.io), 0)
  assert.equal(current.out.some((line) => line.includes('service install" again')), false, current.out.join('\n'))
  assert.match(current.out.join('\n'), /^Upgrades: /m)
})
