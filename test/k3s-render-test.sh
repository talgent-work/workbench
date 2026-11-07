#!/usr/bin/env bash
# Sandboxes run in their own namespace, talgent-sandbox, and NetworkPolicies
# fence only untrusted workloads. The base, rendered offline from its
# placeholders, keeps every sandbox boundary there and grants the platform
# namespace nothing a sandbox needs.
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

TALGENT_IMAGE_TAG=v0.0.0 bash "$root_dir/scripts/deploy/render-k3s.sh" --placeholders \
  "$root_dir/deploy/k3s/workbench" > "$work/rendered.yaml"

RENDERED="$work/rendered.yaml" SERVER="$root_dir/deploy/opensandbox/server.yaml" python3 - <<'PYTHON'
import os, sys
import yaml

docs = [d for d in yaml.safe_load_all(open(os.environ["RENDERED"])) if d]
failures = []

def check(ok, message):
    if not ok:
        failures.append(message)

def where(doc):
    return doc["metadata"].get("namespace")

def named(kind):
    return {(d["metadata"]["name"], where(d)) for d in docs if d["kind"] == kind}

# Gotenberg renders untrusted documents, the only fenced workload besides
# sandboxes; no policy targets a trusted platform service.
for name, namespace in named("NetworkPolicy"):
    if name != "gotenberg-isolation":
        check(namespace == "talgent-sandbox", f"NetworkPolicy {name} is in {namespace}, not talgent-sandbox")
check(("gotenberg-isolation", "talgent") in named("NetworkPolicy"), "Gotenberg keeps its no-egress policy in talgent")
check({"sandbox-boundary", "sandbox-native-api"} <= {n for n, _ in named("NetworkPolicy")},
      "the sandbox policies sandbox-boundary and sandbox-native-api are rendered")

check(("talgent-sandbox", None) in named("Namespace"), "the base creates namespace talgent-sandbox")
# The Cloud's execution quota counts only sandboxes; every namespace still
# gives Pods that declare no requests default ones.
check(named("ResourceQuota") == {("agent-executions", "talgent-sandbox")},
      f"the only ResourceQuota is agent-executions in talgent-sandbox, got {named('ResourceQuota')}")
check({ns for _, ns in named("LimitRange")} == {"talgent", "talgent-sandbox"},
      f"each namespace has a LimitRange, got {named('LimitRange')}")
# controlplane reads the quota and observes BatchSandboxes only where they are.
check(("talgent-controlplane", "talgent-sandbox") in named("Role"), "controlplane's Role is in talgent-sandbox")
check(("talgent-controlplane", "talgent") not in named("Role"), "talgent keeps no Role for controlplane")
binding = next((d for d in docs if d["kind"] == "RoleBinding" and d["metadata"]["name"] == "talgent-controlplane"), None)
check(binding is not None and where(binding) == "talgent-sandbox"
      and binding["subjects"] == [{"kind": "ServiceAccount", "name": "talgent-controlplane", "namespace": "talgent"}],
      "controlplane's RoleBinding in talgent-sandbox binds talgent's ServiceAccount")
# Sandboxes mount claims of the names Storage grants, bound to PVs of their own.
claims = {(d["metadata"]["name"], where(d)): d for d in docs if d["kind"] == "PersistentVolumeClaim"}
volumes = {d["metadata"]["name"]: d for d in docs if d["kind"] == "PersistentVolume"}
for claim in ("talgent-nas-root", "talgent-juicefs-inputs"):
    sandbox_claim = claims.get((claim, "talgent-sandbox"))
    check(sandbox_claim is not None, f"claim {claim} is in talgent-sandbox")
    if sandbox_claim is None:
        continue
    selector = sandbox_claim["spec"]["selector"]["matchLabels"]
    bound = [n for n, v in volumes.items() if selector.items() <= v["metadata"].get("labels", {}).items()]
    check(bound == [f"{claim}-sandbox"], f"claim {claim} in talgent-sandbox selects only PV {claim}-sandbox, got {bound}")
nas, nas_sandbox = volumes["talgent-nas-root"], volumes.get("talgent-nas-root-sandbox")
check(nas_sandbox is not None and nas_sandbox["spec"]["csi"]["volumeAttributes"] == nas["spec"]["csi"]["volumeAttributes"],
      "the sandbox NAS PV mounts the platform's NFS share")
check(nas_sandbox is not None and nas_sandbox["spec"]["csi"]["volumeHandle"] == "talgent-nas-root-sandbox",
      "the sandbox NAS PV has a volume handle of its own")
inputs = volumes.get("talgent-juicefs-inputs-sandbox")
check(inputs is not None and inputs["spec"]["csi"]["nodePublishSecretRef"] == volumes["talgent-juicefs-root"]["spec"]["csi"]["nodePublishSecretRef"],
      "the sandbox inputs PV mounts the platform's JuiceFS file system")
check(inputs is not None and inputs["spec"]["csi"]["volumeHandle"] == "talgent-juicefs-inputs-rustfs-sandbox",
      "the sandbox inputs PV has a volume handle of its own")
check(("talgent-nas-root", "talgent") in claims and ("talgent-juicefs-root", "talgent") in claims,
      "controlplane keeps its claims in talgent")

# The OpenSandbox server, deployed from deploy/opensandbox/server.yaml with
# its permissions from the base, creates sandboxes in talgent-sandbox and can
# touch namespaced resources only there.
server = [d for d in yaml.safe_load_all(open(os.environ["SERVER"])) if d]
config = next(d for d in server if d["kind"] == "ConfigMap" and d["metadata"]["name"] == "opensandbox-config")
check('namespace = "talgent-sandbox"' in config["data"]["config.toml"], "OpenSandbox creates sandboxes in talgent-sandbox")
server_account = {"kind": "ServiceAccount", "name": "opensandbox", "namespace": "talgent"}
everything = docs + server
def granted(binding_kind, role_kind):
    return [(b, r) for b in everything if b["kind"] == binding_kind and server_account in b.get("subjects", [])
            for r in everything if r["kind"] == role_kind and r["metadata"]["name"] == b["roleRef"]["name"]
            and b["roleRef"]["kind"] == role_kind and where(r) == where(b)]
cluster_reads = {("", "namespaces"), ("node.k8s.io", "runtimeclasses")}
for _, role in granted("ClusterRoleBinding", "ClusterRole"):
    for rule in role["rules"]:
        resources = {(g, r) for g in rule["apiGroups"] for r in rule["resources"]}
        check(resources <= cluster_reads and set(rule["verbs"]) <= {"get", "list", "watch"},
              f"ClusterRole {role['metadata']['name']} grants the OpenSandbox server {sorted(rule['verbs'])} on {sorted(resources)} at cluster scope")
namespaced = granted("RoleBinding", "Role")
check([where(r) for _, r in namespaced] == ["talgent-sandbox"], "the OpenSandbox server has one Role, in talgent-sandbox")
check(any("pods" in rule["resources"] and "create" in rule["verbs"] for _, r in namespaced for rule in r["rules"]),
      "the OpenSandbox server's Role lets it create sandbox Pods")

# The sandboxes' namespace is configured as SANDBOX_NAMESPACE only.
check("K8S_NAMESPACE" not in open(os.environ["RENDERED"]).read(), "the rendered base still names K8S_NAMESPACE")

if failures:
    print("\n".join(failures), file=sys.stderr)
    sys.exit(1)
PYTHON
echo "k3s-render: ok"
