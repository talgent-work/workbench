#!/usr/bin/env bash
# Exercise the assembled product, using only an isolated internal Docker network.
# Usage: bash test/sandbox-marketplace-smoke.sh <local-sandbox-image>
set -euo pipefail

TASK_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TASK_IMAGE="${1:?pass an already-built local sandbox image}"
TASK_CLI_VERSION="$(sed -n 's/.*@anthropic-ai\/claude-code@\([^ ]*\).*/\1/p' "$TASK_ROOT/deploy/sandbox/Dockerfile.base")"
test -n "$TASK_CLI_VERSION"
TASK_GITHUB_MCP_VERSION="$(sed -n 's/^ARG GITHUB_MCP_VERSION=//p' "$TASK_ROOT/deploy/sandbox/Dockerfile.base")"
test -n "$TASK_GITHUB_MCP_VERSION"
docker image inspect "$TASK_IMAGE" >/dev/null
TASK_NETWORK="$(docker network create --internal "talgent-marketplace-smoke-$$")"
TASK_CONTAINER="talgent-marketplace-smoke-$$"
TASK_FIXTURE="$(mktemp -d)"
mkdir -p "$TASK_FIXTURE/.claude-plugin"
cp -a "$TASK_ROOT/plugins/talgent-runtime" "$TASK_FIXTURE/talgent-runtime"
printf '%s\n' '{"name":"talgent-smoke","owner":{"name":"Fixture"},"plugins":[{"name":"talgent-runtime","source":"./talgent-runtime"}]}' > "$TASK_FIXTURE/.claude-plugin/marketplace.json"
chmod -R a+rX "$TASK_FIXTURE"
cleanup() {
  docker rm -f "$TASK_CONTAINER" >/dev/null 2>&1 || true
  docker network rm "$TASK_NETWORK" >/dev/null
  rm -rf "$TASK_FIXTURE"
}
trap cleanup EXIT
# The native installer rejects loopback archive URLs. Use the container's private
# address with a fixture CA; do not disable TLS verification or allow public egress.
docker run -d --pull never --name "$TASK_CONTAINER" --memory 2g --cpus 2 \
  --mount "type=bind,src=$TASK_FIXTURE,dst=/tmp/fixture-marketplace,readonly" \
  --network "$TASK_NETWORK" -e "CLAUDE_EXPECTED_VERSION=$TASK_CLI_VERSION" -e "GITHUB_MCP_EXPECTED_VERSION=$TASK_GITHUB_MCP_VERSION" \
  "$TASK_IMAGE" sleep 180 >/dev/null
timeout 150 docker exec -i "$TASK_CONTAINER" sh -s <<'CONTAINER'
set -eu
test "$(id -u)" = 1000
test "$(claude --version)" = "$CLAUDE_EXPECTED_VERSION (Claude Code)"
gh --version
github-mcp-server --version | grep -Fx "Version: $GITHUB_MCP_EXPECTED_VERSION"
# This is a fixed distribution fixture. Platform acquisition/publication is
# verified in controlplane integration tests, not by running a publisher here.
mkdir -p /tmp/catalog-output/archives
task_catalog_ip=$(node -e 'const os=require("node:os"); process.stdout.write(Object.values(os.networkInterfaces()).flat().find(i=>i.family==="IPv4"&&!i.internal).address)')
export TASK_CATALOG_IP="$task_catalog_ip"
openssl req -x509 -newkey rsa:2048 -nodes -keyout /tmp/catalog-key.pem -out /tmp/catalog-cert.pem -days 1 -subj /CN=catalog-fixture -addext "subjectAltName=IP:$task_catalog_ip" >/dev/null 2>&1
python3 - <<'PACKAGE'
import hashlib,json,os,pathlib,zipfile
root=pathlib.Path('/tmp/fixture-marketplace/talgent-runtime')
archive=pathlib.Path('/tmp/catalog-output/plugin.zip')
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as package:
    for file in root.rglob('*'):
        if file.is_file(): package.write(file,file.relative_to(root))
digest=hashlib.sha256(archive.read_bytes()).hexdigest()
archive.rename(archive.parent/'archives'/(digest+'.zip'))
manifest=json.loads((root/'.claude-plugin/plugin.json').read_text())
entry={'name':manifest['name'],'version':manifest['version'],'source':{'source':'archive','url':'https://'+os.environ['TASK_CATALOG_IP']+':39443/archives/'+digest+'.zip','sha256':digest}}
pathlib.Path('/tmp/catalog-entry.json').write_text(json.dumps(entry))
PACKAGE
CONTAINER

# Native installation consumes the fixed archive entry in an independent HOME.
timeout 60 docker exec -i "$TASK_CONTAINER" sh -s <<'CONSUMER'
set -eu
export TASK_CATALOG_IP="$(node -e 'const os=require("node:os"); process.stdout.write(Object.values(os.networkInterfaces()).flat().find(i=>i.family==="IPv4"&&!i.internal).address)')"
node - <<'SERVER' &
const fs=require('node:fs');
require('node:https').createServer({key:fs.readFileSync('/tmp/catalog-key.pem'),cert:fs.readFileSync('/tmp/catalog-cert.pem')},(req,res)=>{
 if(!/^\/archives\/[a-f0-9]{64}\.zip$/.test(req.url)){res.writeHead(404).end();return;}
 const file='/tmp/catalog-output'+req.url;
 if(!fs.existsSync(file)){res.writeHead(404).end();return;}
 res.writeHead(200,{'Content-Type':'application/zip'});
 fs.createReadStream(file).pipe(res);
}).listen(39443,process.env.TASK_CATALOG_IP,()=>fs.writeFileSync('/tmp/catalog-server-ready','ready'));
SERVER
task_catalog_server=$!
trap 'kill "$task_catalog_server" 2>/dev/null || true' EXIT
node -e 'const fs=require("node:fs"); const end=Date.now()+5000; const check=()=>{if(fs.existsSync("/tmp/catalog-server-ready"))return; if(Date.now()>end)process.exit(1); setTimeout(check,20)}; check()'

mkdir -p /tmp/published-marketplace/.claude-plugin /tmp/smoke-work /tmp/smoke-home
node - <<'MANIFEST'
const fs=require('node:fs');
const entry=JSON.parse(fs.readFileSync('/tmp/catalog-entry.json'));
fs.writeFileSync('/tmp/published-marketplace/.claude-plugin/marketplace.json',JSON.stringify({name:'talgent-smoke-published',owner:{name:'Fixture'},plugins:[entry]}));
MANIFEST
export CLAUDE_CONFIG_DIR=/tmp/smoke-home/.claude
export NODE_EXTRA_CA_CERTS=/tmp/catalog-cert.pem
export DISABLE_AUTOUPDATER=1
cd /tmp/smoke-work
env HOME=/tmp/smoke-home claude plugin marketplace add /tmp/published-marketplace
env HOME=/tmp/smoke-home claude plugin install talgent-runtime@talgent-smoke-published --scope project
node - <<'VERIFY'
const fs=require('node:fs');
const assert=require('node:assert/strict');
const installed=JSON.parse(fs.readFileSync('/tmp/smoke-home/.claude/plugins/installed_plugins.json')).plugins['talgent-runtime@talgent-smoke-published'][0];
assert.equal(installed.scope,'project');
assert.equal(installed.projectPath,'/tmp/smoke-work');
assert.ok(fs.existsSync(installed.installPath+'/agents/talgent-work-executor.md'));
console.log('Published release installed natively in independent Work scope');
VERIFY
CONSUMER
