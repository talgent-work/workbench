// Prepares a Work's environment inside its sandbox before the Harness starts:
// native Plugin installation, then checkout of the planned
// repositories. It adapts the native CLI and git; it is not a package fetcher.
// Output is one bounded report line; CLI and git diagnostics never leave the
// sandbox, because they can echo remote URLs and credential errors.
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
// place is "machine" on a Managed Computer, whose own git credentials serve
// the checkouts; it is absent in a Cloud sandbox.
const [binary, encodedCatalog, receipt, encodedPlan, budget, place] = process.argv.slice(1);
const onMachine = place === 'machine';
const catalog = JSON.parse(encodedCatalog);
// The plan is null when this start does not check out (Resume after a checkout
// that already ended, or a Work without an initial request).
const plan = encodedPlan ? JSON.parse(encodedPlan) : null;
// Leave time to write the report before the outer timeout ends this process.
const deadline = Date.now() + Math.max(0, Number(budget) * 1000 - 5000);
const remaining = () => Math.max(0, deadline - Date.now());
const workspace = process.cwd();
const root = path.join(workspace, '.claude', 'talgent', catalog.name);
// One report per allocation: a re-run of a finished preparation reports it.
const reportPath = path.join(root, 'report-' + receipt + '.json');
const settingsPath = path.join(workspace, '.claude', 'settings.local.json');
// A Plugin package cache of the Work's own (CLAUDE_CODE_PLUGIN_CACHE_DIR, on a
// Managed Computer) has one installer; the cache in HOME is shared.
const sharedCache = !process.env.CLAUDE_CODE_PLUGIN_CACHE_DIR;
const lockPath = path.join(process.env.HOME, '.claude', 'talgent-plugin-install.lock');
// Plans name repositories by their path in the Cloud layout of the Work's
// workspace, which is this process's working directory. The Work root is
// where that layout lives: /workspace in a sandbox, TALGENT_WORK_ROOT on a
// Managed Computer.
const layoutRoot = '/workspace';
const checkoutRoot = layoutRoot + '/repos';
const local = (target) => path.join(workspace, path.posix.relative(layoutRoot, target));
const stagingRoot = path.join(workspace, '.talgent', 'checkouts');
// Parallel file writers per checkout; sandbox virtiofsd serves up to 16
// requests at once (deploy/kata/values.yaml).
const checkoutWorkers = 8;
const read = (file) => {
  try { return JSON.parse(fs.readFileSync(file, 'utf8')); }
  catch (error) { if (error.code === 'ENOENT') return {}; throw error; }
};
const save = (file, value) => {
  const temporary = file + '.talgent-' + process.pid;
  fs.writeFileSync(temporary, JSON.stringify(value), { mode: 0o600 });
  fs.renameSync(temporary, file);
};
const active = new Set();
for (const [signal, exitCode] of [['SIGTERM', 143], ['SIGINT', 130]]) {
  process.once(signal, () => { for (const kill of active) kill(); process.exit(exitCode); });
}
// capture starts one child in its own process group and kills the whole group
// at its limit or when this process is asked to end. It resolves to the exit
// code, or null when the child could not run or was killed at its limit, and
// to at most 4 KiB of its standard output when asked for it.
const capture = (command, args, options, limit, keepOutput) => new Promise(resolve => {
  const child = spawn(command, args, { stdio: ['ignore', keepOutput ? 'pipe' : 'ignore', 'ignore'], detached: true, ...options });
  let expired = false;
  let output = '';
  if (keepOutput) child.stdout.on('data', chunk => { if (output.length < 4096) output += chunk; });
  // A group that is already gone, or one this process may no longer signal,
  // has nothing left to end; the report must still be written.
  const kill = () => {
    if (!child.pid) return;
    try { process.kill(-child.pid, 'SIGKILL'); } catch {}
  };
  active.add(kill);
  const timer = setTimeout(() => { expired = true; kill(); }, Math.max(0, limit));
  child.once('error', () => { clearTimeout(timer); active.delete(kill); resolve({ code: null, output: '' }); });
  child.once('close', code => { clearTimeout(timer); kill(); active.delete(kill); resolve({ code: expired ? null : code, output: output.trim() }); });
});
const run = async (command, args, options, limit) => (await capture(command, args, options, limit, false)).code;

// The HOME-wide native package cache admits one installer at a time. The lock
// covers Plugin installation only, so checkout never waits for another Work.
const acquireInstallLock = () => new Promise(resolve => {
  fs.mkdirSync(path.dirname(lockPath), { recursive: true, mode: 0o700 });
  const wait = String(Math.max(1, Math.floor(remaining() / 1000)));
  const holder = spawn('flock', ['--no-fork', '-w', wait, lockPath, 'sh', '-c', 'echo locked; exec cat >/dev/null'], { stdio: ['pipe', 'pipe', 'ignore'] });
  const release = () => { holder.stdin.end(); };
  active.add(() => holder.kill('SIGKILL'));
  let locked = false;
  holder.stdout.once('data', () => { locked = true; resolve(release); });
  holder.once('error', () => resolve(null));
  holder.once('exit', () => { if (!locked) resolve(null); });
});

// One native install clones the package and, when it ships a lockfile, runs
// the CLI's own dependency install with its 60 s limit. 180 s per command keeps
// plugins that would install; the preparation bound still limits all of them.
const invoke = async (...args) => {
  const env = { ...process.env, DISABLE_AUTOUPDATER: '1' };
  return await run(binary, args, { cwd: workspace, env }, Math.min(180000, remaining())) === 0;
};

async function installPlugins() {
  const release = catalog.plugins.length === 0 || !sharedCache ? () => {} : await acquireInstallLock();
  if (!release) return catalog.plugins.map(plugin => ({ slug: plugin.name, installed: false, reason: 'installation_failed' }));
  try {
    fs.mkdirSync(path.join(root, '.claude-plugin'), { recursive: true, mode: 0o700 });
    save(path.join(root, '.claude-plugin', 'marketplace.json'), catalog);
    // Work selection is authoritative for this workspace. Explicit false entries
    // also mask stale project-scoped selections from an earlier preparation.
    const project = read(path.join(workspace, '.claude', 'settings.json'));
    const settings = read(settingsPath);
    const enabled = Object.fromEntries(Object.keys({ ...project.enabledPlugins, ...settings.enabledPlugins }).map(id => [id, false]));
    for (const plugin of catalog.plugins) enabled[plugin.name + '@' + catalog.name] = false;
    settings.enabledPlugins = enabled;
    save(settingsPath, settings);
    const available = catalog.plugins.length === 0 || await invoke('plugin', 'marketplace', 'add', '--scope', 'project', root);
    const results = [];
    for (const plugin of catalog.plugins) {
      const id = plugin.name + '@' + catalog.name;
      // Install leaves an installed Plugin at its recorded version when the
      // manifest above now pins another release (a reused HOME and workspace);
      // update moves the record to the pinned release and is a no-op otherwise.
      const installed = available && await invoke('plugin', 'install', '--scope', 'project', id)
        && await invoke('plugin', 'update', '--scope', 'project', id);
      enabled[id] = installed;
      results.push({ slug: plugin.name, installed, reason: installed ? '' : (available ? 'installation_failed' : 'marketplace_unavailable') });
    }
    // Native install writes project settings. Retain our local scope override, and
    // preserve unrelated local settings. No parameter values are persisted here.
    const current = read(settingsPath);
    current.enabledPlugins = enabled;
    save(settingsPath, current);
    return results;
  } finally {
    release();
  }
}

// git runs without hooks, fsmonitor, redirects, prompts or inherited helpers.
// Remote access goes through the sandbox's Credential Proxy, whose Vault holds
// the Work's per-repository credentials; no credential reaches this process.
// On a Managed Computer the machine's own git credentials serve the Work, so
// its credential helpers and system configuration are kept.
const gitArgs = (args) => ['-c', 'core.hooksPath=/dev/null', '-c', 'core.fsmonitor=false', '-c', 'http.followRedirects=false', ...(onMachine ? [] : ['-c', 'credential.helper=']), ...args];
const gitOptions = (cwd) => ({ cwd, env: { ...process.env, GIT_TERMINAL_PROMPT: '0', ...(onMachine ? {} : { GIT_CONFIG_NOSYSTEM: '1' }), LANG: 'C.UTF-8' } });
const git = (cwd, ...args) => run('git', gitArgs(args), gitOptions(cwd), remaining());
const gitOutput = async (cwd, ...args) => {
  const result = await capture('git', gitArgs(args), gitOptions(cwd), Math.min(30000, remaining()), true);
  return result.code === 0 ? result.output : null;
};
const exists = (target) => {
  try { return fs.lstatSync(target); }
  catch (error) { if (error.code === 'ENOENT') return null; throw error; }
};
const withinCheckoutRoot = (target) => typeof target === 'string' && path.posix.normalize(target) === target && (target === checkoutRoot || target.startsWith(checkoutRoot + '/'));

// A repository already at its target is kept as it is: its branch, index and
// working tree belong to the Work, so nothing is fetched or checked out again.
async function checkOut(repository) {
  const targetPath = repository.targetPath;
  const outcome = (checkedOut, reason) => ({ repositoryId: repository.repositoryId, targetPath, checkedOut, reason });
  if (!withinCheckoutRoot(targetPath)) return outcome(false, 'target_conflict');
  const target = local(targetPath);
  const existing = exists(target);
  if (existing) {
    const origin = existing.isDirectory() && exists(path.join(target, '.git')) ? await gitOutput(target, 'config', '--local', '--get', 'remote.origin.url') : null;
    return origin === repository.cloneUrl ? outcome(true, '') : outcome(false, 'target_conflict');
  }
  if (remaining() === 0) return outcome(false, 'timed_out');
  for (const branch of [repository.baseBranch, repository.workBranch]) {
    if (await git(workspace, 'check-ref-format', '--branch', branch) !== 0) return outcome(false, 'branch_unavailable');
  }
  fs.mkdirSync(stagingRoot, { recursive: true, mode: 0o700 });
  const staging = fs.mkdtempSync(path.join(stagingRoot, 'repo-'));
  const clone = path.join(staging, 'repo');
  try {
    const cloned = await git(staging, 'clone', '--no-checkout', '--origin', 'origin', '--', repository.cloneUrl, clone);
    if (cloned !== 0) return outcome(false, remaining() === 0 ? 'timed_out' : 'checkout_unavailable');
    // Each file write in the workspace waits for its upload to shared storage,
    // so this and every later checkout in the Work writes files in parallel;
    // git still writes small trees serially.
    if (await git(clone, 'config', '--local', 'checkout.workers', String(checkoutWorkers)) !== 0) return outcome(false, 'checkout_unavailable');
    const base = await git(clone, 'show-ref', '--verify', '--quiet', 'refs/remotes/origin/' + repository.baseBranch);
    if (base === 0) {
      if (await git(clone, 'checkout', '--no-track', '-b', repository.workBranch, 'refs/remotes/origin/' + repository.baseBranch, '--') !== 0) return outcome(false, remaining() === 0 ? 'timed_out' : 'checkout_unavailable');
    } else if (base === 1 && await git(clone, 'show-ref', '--quiet') === 1) {
      // Only a genuinely empty repository starts the work branch unborn.
      if (await git(clone, 'symbolic-ref', 'HEAD', 'refs/heads/' + repository.workBranch) !== 0) return outcome(false, 'checkout_unavailable');
    } else {
      return outcome(false, base === null && remaining() === 0 ? 'timed_out' : 'branch_unavailable');
    }
    if (await git(clone, 'config', '--local', 'http.followRedirects', 'false') !== 0) return outcome(false, 'checkout_unavailable');
    fs.mkdirSync(path.dirname(target), { recursive: true });
    if (exists(target)) return outcome(false, 'target_conflict');
    fs.renameSync(clone, target);
    return outcome(true, '');
  } finally {
    fs.rmSync(staging, { recursive: true, force: true });
  }
}

async function checkOutRepositories() {
  if (plan === null) return null;
  // Staging left by an interrupted run never becomes a checkout.
  fs.rmSync(stagingRoot, { recursive: true, force: true });
  const outcomes = [];
  for (const repository of plan) {
    let outcome;
    try { outcome = await checkOut(repository); }
    catch { outcome = { repositoryId: repository.repositoryId, targetPath: repository.targetPath, checkedOut: false, reason: 'checkout_unavailable' }; }
    outcomes.push(outcome);
    // Each settled repository is reported at once, so an outer timeout that
    // ends this process before its report still leaves finished checkouts known.
    process.stdout.write(JSON.stringify({ checkout: outcome }) + '\n');
  }
  return outcomes;
}

async function main() {
  const finished = read(reportPath);
  if (Array.isArray(finished.results)) {
    process.stdout.write(JSON.stringify(finished) + '\n');
    return;
  }
  fs.mkdirSync(root, { recursive: true, mode: 0o700 });
  const report = { results: await installPlugins(), repositories: await checkOutRepositories() };
  save(reportPath, report);
  process.stdout.write(JSON.stringify(report) + '\n');
}
main().catch(() => { process.exitCode = 1; });
