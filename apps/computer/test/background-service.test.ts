import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, posix, win32 } from 'node:path'
import { afterEach, beforeEach, test } from 'node:test'
import { resolvePaths } from '../src/paths.ts'
import { serviceManager, serviceSpec, type ServiceHost } from '../src/service/command.ts'
import { launchAgentPlist } from '../src/service/launchd.ts'
import type { CommandResult, RunCommand, ServiceSpec } from '../src/service/manager.ts'
import { systemdUnit } from '../src/service/systemd.ts'
import { logonTask, TASK_NAME, wrapperScript } from '../src/service/task-scheduler.ts'
import { SUPERVISED_ENV } from '../src/upgrade/restart.ts'
import { posixOnly } from './support/platform.ts'

const SPEC: ServiceSpec = {
  program: '/opt/node 24/bin/node',
  args: ['/usr/lib/node_modules/@talgent/computer/dist/talgent-computer.mjs', 'run'],
  env: { PATH: '/opt/homebrew/bin:/usr/bin', XDG_CONFIG_HOME: '/home/a/.config', XDG_DATA_HOME: '/home/a/.local/share' },
  workingDir: '/home/a',
  logFile: '/home/a/.local/share/talgent-computer/logs/service.log',
}

// What serviceSpec writes since self-upgrade.
const SUPERVISED_SPEC: ServiceSpec = { ...SPEC, env: { ...SPEC.env, [SUPERVISED_ENV]: '1' } }

let dir: string
beforeEach(async () => { dir = await mkdtemp(join(tmpdir(), 'background-service-')) })
afterEach(async () => { await rm(dir, { recursive: true, force: true }) })

// Records every platform tool call and answers from a script.
function tools(answer: (command: string, args: string[]) => Partial<CommandResult> = () => ({})) {
  const calls: string[] = []
  const run: RunCommand = async (command, args) => {
    calls.push([command, ...args].join(' '))
    return { code: 0, stdout: '', stderr: '', ...answer(command, args) }
  }
  return { calls, run }
}

function host(platform: NodeJS.Platform, run: RunCommand, env: NodeJS.ProcessEnv = {}): ServiceHost {
  return { platform, env, node: '/usr/bin/node', program: '/pkg/talgent-computer.mjs', run, account: { home: dir, uid: 501, username: 'a' } }
}

test('the service runs plain "run" with this installation\'s node, program, PATH and files', () => {
  const paths = resolvePaths({ XDG_CONFIG_HOME: '/cfg', XDG_DATA_HOME: '/data' }, '/home/a', 'linux')
  assert.deepEqual(serviceSpec(host('linux', tools().run, { PATH: '/bin', HOME: '/elsewhere' }), paths), {
    program: '/usr/bin/node',
    args: ['/pkg/talgent-computer.mjs', 'run'],
    env: { PATH: '/bin', XDG_CONFIG_HOME: '/cfg', XDG_DATA_HOME: '/data', TALGENT_COMPUTER_SUPERVISED: '1' },
    workingDir: dir,
    logFile: '/data/talgent-computer/logs/service.log',
  }, 'the paths follow the service platform\'s rules, whatever this host is')
  // The service resolves exactly the files the installing command uses.
  const seen = resolvePaths({ XDG_CONFIG_HOME: '/cfg', XDG_DATA_HOME: '/data' }, '/other', 'linux')
  assert.deepEqual(seen, paths)
})

test('systemd keeps the run going, except after a run that ended for good, and leaves Work processes alone', () => {
  const unit = systemdUnit({ ...SPEC, env: { PATH: '/a$b%c"d' } })
  assert.match(unit, /^ExecStart="\/opt\/node 24\/bin\/node" "\/usr\/lib\/node_modules\/@talgent\/computer\/dist\/talgent-computer.mjs" "run"$/m)
  assert.match(unit, /^Environment="PATH=\/a\$\$b%%c\\"d"$/m)
  assert.match(unit, /^Restart=on-failure$/m)
  assert.match(unit, /^RestartPreventExitStatus=3 4 5 7$/m)
  assert.match(unit, /^KillMode=process$/m)
  assert.match(unit, /^WantedBy=default.target$/m)
})

test('on Linux, install writes the user unit, enables and starts it; uninstall removes it', async () => {
  let loaded = false
  const { calls, run } = tools((_command, args) => {
    if (args[1] === 'show') return { stdout: `LoadState=${loaded ? 'loaded' : 'not-found'}\nActiveState=active\nMainPID=42\nExecMainStatus=0\n` }
    if (args[1] === 'enable') loaded = true
    return {}
  })
  const manager = serviceManager(host('linux', run), resolvePaths({}, dir, 'linux'))
  await manager.install(SPEC)
  const unitFile = join(dir, '.config', 'systemd', 'user', 'talgent-computer.service')
  assert.equal(await readFile(unitFile, 'utf8'), systemdUnit(SPEC))
  assert.deepEqual(calls, [
    'systemctl --user daemon-reload',
    'systemctl --user enable talgent-computer.service',
    'systemctl --user restart talgent-computer.service',
  ])
  assert.deepEqual(await manager.status(), {
    installed: true, running: true, pid: 42, lastExit: 0, supervisesUpgrades: false, definition: unitFile, logs: 'journalctl --user -u talgent-computer.service',
  }, 'a definition without the supervised variable predates self-upgrade')
  await manager.install(SUPERVISED_SPEC)
  assert.equal((await manager.status()).supervisesUpgrades, true)
  calls.length = 0
  assert.equal(await manager.uninstall(), true)
  assert.ok(calls.includes('systemctl --user disable --now talgent-computer.service'))
  assert.equal(existsSync(unitFile), false)
  loaded = false
  assert.equal(await manager.uninstall(), false)
  assert.equal((await manager.status()).installed, false)
})

test('a failing platform tool fails the install with its own words', async () => {
  const { run } = tools((_command, args) => args[1] === 'enable' ? { code: 1, stderr: 'Failed to connect to bus' } : {})
  const manager = serviceManager(host('linux', run), resolvePaths({}, dir, 'linux'))
  await assert.rejects(manager.install(SPEC), /systemctl --user enable talgent-computer.service failed: Failed to connect to bus/)
})

test('launchd runs the agent at load, restarts it after failures, and writes its output to the log file', () => {
  const plist = launchAgentPlist({ ...SPEC, env: { PATH: '/a&b' } })
  assert.match(plist, /<key>Label<\/key>\n {2}<string>work.talgent.computer<\/string>/)
  assert.match(plist, /<string>\/opt\/node 24\/bin\/node<\/string>\n {4}<string>\/usr\/lib\/node_modules\/@talgent\/computer\/dist\/talgent-computer.mjs<\/string>\n {4}<string>run<\/string>/)
  assert.match(plist, /<key>PATH<\/key>\n {4}<string>\/a&amp;b<\/string>/)
  assert.match(plist, /<key>SuccessfulExit<\/key>\n {4}<false\/>/)
  assert.match(plist, /<key>StandardErrorPath<\/key>\n {2}<string>\/home\/a\/.local\/share\/talgent-computer\/logs\/service.log<\/string>/)
})

// The sh between launchd and the run: its exit status decides whether
// launchd starts the run again.
test('the launchd wrapper turns a run that ended for good into a stop and passes others through', { skip: posixOnly('runs /bin/sh') }, () => {
  const plist = launchAgentPlist(SPEC)
  const script = /<string>-c<\/string>\n {4}<string>([\s\S]*?)<\/string>/.exec(plist)?.[1]
    ?.replace(/&quot;/g, '"').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&')
  assert.ok(script !== undefined)
  const status = (code: number) => {
    try {
      execFileSync('/bin/sh', ['-c', script, '/bin/sh', '-c', `exit ${code}`], { stdio: 'ignore' })
      return 0
    } catch (error) {
      return (error as { status: number }).status
    }
  }
  assert.equal(status(0), 0)
  assert.equal(status(1), 1, 'a failure is restarted')
  for (const code of [3, 4, 5, 7]) assert.equal(status(code), 0, `exit ${code} ends the service`)
})

test('on macOS, install bootstraps the agent in the login session; status reads launchctl', async () => {
  const { calls, run } = tools((_command, args) => args[0] === 'print'
    ? { stdout: 'work.talgent.computer = {\n\tstate = running\n\tpid = 77\n\tlast exit code = (never exited)\n}\n' }
    : {})
  const paths = resolvePaths({}, dir, 'darwin')
  const manager = serviceManager(host('darwin', run), paths)
  const spec = { ...SPEC, logFile: posix.join(paths.dataDir, 'logs', 'service.log') }
  await manager.install(spec)
  const plistFile = join(dir, 'Library', 'LaunchAgents', 'work.talgent.computer.plist')
  assert.equal(await readFile(plistFile, 'utf8'), launchAgentPlist(spec))
  assert.deepEqual(calls, ['launchctl bootout gui/501/work.talgent.computer', `launchctl bootstrap gui/501 ${plistFile}`])
  const state = await manager.status()
  assert.deepEqual(state, {
    installed: true, running: true, pid: 77, lastExit: undefined, supervisesUpgrades: false, definition: plistFile,
    logs: posix.join(paths.dataDir, 'logs', 'service.log'),
  })
  await manager.install({ ...SUPERVISED_SPEC, logFile: spec.logFile })
  assert.equal((await manager.status()).supervisesUpgrades, true)
})

test('the Windows wrapper reruns the command after a failure, stops after one that ended for good, and logs', () => {
  const script = wrapperScript({ ...SPEC, env: { PATH: 'C:\\50%\\bin' } })
  const lines = script.split('\r\n')
  assert.ok(lines.includes('set "PATH=C:\\50%%\\bin"'))
  assert.ok(lines.includes('"/opt/node 24/bin/node" "/usr/lib/node_modules/@talgent/computer/dist/talgent-computer.mjs" "run" >> "/home/a/.local/share/talgent-computer/logs/service.log" 2>&1'))
  for (const code of [0, 3, 4, 5, 7]) assert.ok(lines.includes(`if %code%==${code} exit /b 0`))
  assert.equal(lines.at(-2), 'goto run')
})

test('the logon task starts at the person\'s logon, unelevated, with no window and no time limit', () => {
  const task = logonTask(SPEC, 'DESK\\a', 'C:\\data\\service\\talgent-computer-service.cmd')
  assert.match(task, /<LogonTrigger>\s*<Enabled>true<\/Enabled>\s*<UserId>DESK\\a<\/UserId>/)
  assert.match(task, /<LogonType>InteractiveToken<\/LogonType>\s*<RunLevel>LeastPrivilege<\/RunLevel>/)
  assert.match(task, /<ExecutionTimeLimit>PT0S<\/ExecutionTimeLimit>/)
  assert.match(task, /<Command>conhost.exe<\/Command>\s*<Arguments>--headless cmd.exe \/d \/s \/c &quot;&quot;C:\\data\\service\\talgent-computer-service.cmd&quot;&quot;<\/Arguments>/)
})

test('on Windows, install registers and starts the task; uninstall ends and deletes it', { skip: process.platform !== 'win32' && 'writes the task files at Windows paths' }, async () => {
  let registered = false
  const { calls, run } = tools((command, args) => {
    if (command === 'powershell.exe') return registered ? { stdout: 'Running|267009\r\n' } : { code: 3 }
    if (args[0] === '/Create') registered = true
    if (args[0] === '/Delete') registered = false
    return {}
  })
  const paths = resolvePaths({ APPDATA: join(dir, 'roaming'), LOCALAPPDATA: join(dir, 'local') }, dir, 'win32')
  const manager = serviceManager(host('win32', run, { USERDOMAIN: 'DESK' }), paths)
  await manager.install({ ...SPEC, logFile: win32.join(paths.dataDir, 'logs', 'service.log') })
  const serviceDir = win32.join(paths.dataDir, 'service')
  const definition = await readFile(win32.join(serviceDir, 'talgent-computer-task.xml'))
  assert.deepEqual([...definition.subarray(0, 2)], [0xff, 0xfe], 'the definition is UTF-16')
  assert.match(definition.subarray(2).toString('utf16le'), /<UserId>DESK\\a<\/UserId>/)
  assert.deepEqual(calls, [
    `schtasks /End /TN ${TASK_NAME}`,
    `schtasks /Create /TN ${TASK_NAME} /XML ${win32.join(serviceDir, 'talgent-computer-task.xml')} /F`,
    `schtasks /Run /TN ${TASK_NAME}`,
  ])
  const state = await manager.status()
  assert.equal(state.installed, true)
  assert.equal(state.running, true)
  assert.equal(state.supervisesUpgrades, false)
  await manager.install({ ...SUPERVISED_SPEC, logFile: win32.join(paths.dataDir, 'logs', 'service.log') })
  assert.equal((await manager.status()).supervisesUpgrades, true)
  calls.length = 0
  assert.equal(await manager.uninstall(), true)
  assert.deepEqual(calls.filter((call) => call.startsWith('schtasks')), [`schtasks /End /TN ${TASK_NAME}`, `schtasks /Delete /TN ${TASK_NAME} /F`])
  assert.equal(existsSync(win32.join(serviceDir, 'talgent-computer-service.cmd')), false)
  assert.equal((await manager.status()).installed, false)
})

test('other platforms have no background service', () => {
  assert.throws(() => serviceManager(host('freebsd', tools().run), resolvePaths({}, dir, 'linux')), /not supported on freebsd/)
})

test('ending the Windows task ends the batch file, then the run it started, each alone', { skip: process.platform !== 'win32' && 'writes the run record at a Windows path' }, async () => {
  const paths = resolvePaths({ APPDATA: join(dir, 'roaming'), LOCALAPPDATA: join(dir, 'local') }, dir, 'win32')
  await mkdir(win32.join(paths.dataDir, 'state'), { recursive: true })
  await writeFile(win32.join(paths.dataDir, 'state', 'run.json'), JSON.stringify({ pid: 200, ppid: 100 }))
  const { calls, run } = tools((command, args) => {
    if (command === 'powershell.exe') return { stdout: 'Running|0\r\n' }
    if (command === 'tasklist') return { stdout: args[1] === 'PID eq 100' ? '"cmd.exe","100"' : '"notepad.exe","200"' }
    return {}
  })
  await serviceManager(host('win32', run, { USERDOMAIN: 'DESK' }), paths).uninstall()
  assert.deepEqual(calls.filter((call) => call.startsWith('taskkill')), ['taskkill /F /PID 100'], 'a pid another program took since is left alone')
})
