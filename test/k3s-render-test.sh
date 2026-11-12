#!/usr/bin/env bash
# Sandboxes run in their own namespace, talgent-sandbox, and NetworkPolicies
# fence only untrusted workloads. The base, rendered offline from its
# placeholders, keeps every sandbox boundary there and grants the platform
# namespace nothing a sandbox needs. Rendered again with an environment that
# turns every deployment switch away from its default, each switch changes
# what it names and nothing else.
set -euo pipefail

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

TALGENT_IMAGE_TAG=v0.0.0 bash "$root_dir/scripts/deploy/render-k3s.sh" --placeholders \
  "$root_dir/deploy/k3s/workbench" > "$work/rendered.yaml"

# The placeholders with every switch turned, as a deployment's environment
# file outside the repository.
switched=(
  AGENT_CORE_API_ACCESS=disabled
  ADDITIONAL_ENTRY_HOST=entry.example.org
  CONTROLPLANE_PROFILE=example
)
{
  grep -vE "^($(printf '%s\n' "${switched[@]%%=*}" | paste -sd'|'))=" "$root_dir/deploy/k3s/workbench/environment.env"
  printf '%s\n' "${switched[@]}"
} > "$work/switched.env"
TALGENT_ENVIRONMENT_FILE="$work/switched.env" TALGENT_IMAGE_TAG=v0.0.0 \
  bash "$root_dir/scripts/deploy/render-k3s.sh" "$root_dir/deploy/k3s/workbench" > "$work/switched.yaml" 2> "$work/switched.log" \
  || { cat "$work/switched.log" >&2; exit 1; }

# A switch refuses a value it does not take instead of rendering its default.
sed 's/^AGENT_CORE_API_ACCESS=.*/AGENT_CORE_API_ACCESS=off/' "$work/switched.env" > "$work/invalid.env"
if TALGENT_ENVIRONMENT_FILE="$work/invalid.env" TALGENT_IMAGE_TAG=v0.0.0 \
  bash "$root_dir/scripts/deploy/render-k3s.sh" "$root_dir/deploy/k3s/workbench" > /dev/null 2> "$work/invalid.log"; then
  echo "expected: the render refuses AGENT_CORE_API_ACCESS=off" >&2
  exit 1
fi
grep -q "AGENT_CORE_API_ACCESS is enabled or disabled, not 'off'" "$work/invalid.log" || {
  echo "expected: the render names the switch and the value it refuses, got:" >&2
  cat "$work/invalid.log" >&2
  exit 1
}

RENDERED="$work/rendered.yaml" SWITCHED="$work/switched.yaml" SERVER="$root_dir/deploy/opensandbox/server.yaml" python3 - <<'PYTHON'
import json, os, re, sys
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
        check(namespace == "talgent-sandbox", f"NetworkPolicy {name} is in talgent-sandbox, got {namespace}")
check(("gotenberg-isolation", "talgent") in named("NetworkPolicy"), "Gotenberg keeps its no-egress policy in talgent")
# A sandbox reaches the platform only through the entry, at its public
# origin: one policy fences it, opening no platform service.
check({n for n, ns in named("NetworkPolicy") if ns == "talgent-sandbox"} == {"sandbox-boundary"},
      f"sandbox-boundary is the only sandbox policy, got {named('NetworkPolicy')}")

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
for claim in ("talgent-nas-root", "talgent-juicefs-directories"):
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
directories = volumes.get("talgent-juicefs-directories-sandbox")
check(directories is not None and directories["spec"]["csi"]["nodePublishSecretRef"] == volumes["talgent-juicefs-root"]["spec"]["csi"]["nodePublishSecretRef"],
      "the sandbox JuiceFS PV mounts the platform's JuiceFS file system")
check(directories is not None and directories["spec"]["csi"]["volumeHandle"] == "talgent-juicefs-directories-rustfs-sandbox",
      "the sandbox JuiceFS PV has a volume handle of its own")
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
              f"at cluster scope the OpenSandbox server only reads Namespaces and RuntimeClasses, got ClusterRole "
              f"{role['metadata']['name']} granting {sorted(rule['verbs'])} on {sorted(resources)}")
namespaced = granted("RoleBinding", "Role")
check([where(r) for _, r in namespaced] == ["talgent-sandbox"], "the OpenSandbox server has one Role, in talgent-sandbox")
check(any("pods" in rule["resources"] and "create" in rule["verbs"] for _, r in namespaced for rule in r["rules"]),
      "the OpenSandbox server's Role lets it create sandbox Pods")

# The sandboxes' namespace is configured as SANDBOX_NAMESPACE only.
check("K8S_NAMESPACE" not in open(os.environ["RENDERED"]).read(), "the rendered base never names K8S_NAMESPACE")

# The deployment switches, each by default and turned.
switched = [d for d in yaml.safe_load_all(open(os.environ["SWITCHED"])) if d]

def find(rendered, kind, name):
    return next((d for d in rendered if d["kind"] == kind and d["metadata"]["name"] == name), None)

def container_env(rendered, deployment):
    container = find(rendered, "Deployment", deployment)["spec"]["template"]["spec"]["containers"][0]
    return {e["name"]: e for e in container.get("env", [])}

def service_ports(rendered, name):
    return [p["name"] for p in find(rendered, "Service", name)["spec"]["ports"]]

def rules(rendered, route):
    return [r["name"] for r in find(rendered, "HTTPRoute", route)["spec"]["rules"]]

# The entry: Account's Identity Tokens of both Principals are validated by one
# provider, which writes every claim as its header, after one gateway-wide
# rule removed every identity header a client sent; no route keeps a list of
# its own. Capabilities pass through.
identity_headers = {"x-talgent-" + h for h in (
    "authenticated-by", "issuer", "principal", "subject", "username", "email", "display-name", "avatar-url", "roles",
    "project-id", "agent-id", "purpose", "work-subject-kind", "work-subject-id", "represented-user-id",
    "service-id", "delegated-user-id", "delegated-actor-id", "reader-admission",
    "runtime-instance-id", "runtime-purpose", "runtime-owner-ref")}
entry = find(docs, "ClientTrafficPolicy", "identity-headers")
check(entry is not None and where(entry) == "envoy-gateway-system"
      and entry["spec"]["targetRefs"] == [{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "public"}],
      "ClientTrafficPolicy identity-headers targets the Gateway public in its namespace")
removed = set((entry or {}).get("spec", {}).get("headers", {}).get("earlyRequestHeaders", {}).get("remove", []))
check(removed == identity_headers, f"the entry removes every identity header a client sends, got {sorted(removed ^ identity_headers)} differing")
check("x-talgent-space-token" not in removed, "the Space Token, a capability, passes through the entry")
for route in (d for d in docs if d["kind"] == "HTTPRoute"):
    for rule in route["spec"]["rules"]:
        for f in rule.get("filters", []):
            check(not f.get("requestHeaderModifier", {}).get("remove"),
                  f"route {route['metadata']['name']} rule {rule['name']} removes no header of its own")
auth = find(docs, "SecurityPolicy", "web-auth")
providers = auth["spec"]["jwt"]["providers"]
check(len(providers) == 1, "web-auth validates every Principal with one provider")
claims = {c["claim"]: c["header"] for c in providers[0]["claimToHeaders"]}
want_claims = {"principal": "x-talgent-principal", "iss": "x-talgent-issuer", "sub": "x-talgent-subject",
               "preferred_username": "x-talgent-username", "email": "x-talgent-email", "name": "x-talgent-display-name",
               "picture": "x-talgent-avatar-url", "roles": "x-talgent-roles", "project_id": "x-talgent-project-id",
               "agent_id": "x-talgent-agent-id", "purpose": "x-talgent-purpose", "work_subject_kind": "x-talgent-work-subject-kind",
               "work_subject_id": "x-talgent-work-subject-id", "for": "x-talgent-represented-user-id"}
check(claims == want_claims, f"web-auth writes every claim as its header, got {claims}")
targets = {t["sectionName"] for t in auth["spec"]["targetRefs"]}
for section in ("agent-identity-api", "storage-artifact-api", "personal-work-api", "work-file-api", "application-api"):
    check(section in rules(docs, "web-api") and section in targets, f"web-auth validates the web-api rule {section}")
check(find(docs, "BackendTrafficPolicy", "computer-api-rate-limit") is None and "computer-api" not in rules(docs, "computer"),
      "Managed Works reach the platform through the entry, not a computer-api rule")
check(service_ports(docs, "controlplane") == ["http"],
      f"Service controlplane has one port, behind the entry, got {service_ports(docs, 'controlplane')}")

# Agents have one way in, the entry: no native API object, port, route or
# switch is rendered, and one sandbox policy reaches only the entry.
def native_ports(node):
    """Every port field set to 8081, and every string that addresses :8081."""
    if isinstance(node, dict):
        for key, value in node.items():
            if key in ("port", "containerPort", "targetPort") and value in (8081, "8081"):
                yield f"{key}: {value}"
            yield from native_ports(value)
    elif isinstance(node, list):
        for item in node:
            yield from native_ports(item)
    elif isinstance(node, str) and re.search(r":8081(?![0-9])", node):
        yield node

for rendered, how in ((docs, "by default"), (switched, "with every switch set")):
    text = json.dumps(rendered)
    for remnant in ("native-api", "native-authentication", "NATIVE_API", "GATEWAY_TOKEN", "gateway-token",
                    "sandbox-native-api", "x-talgent-work-credential"):
        check(remnant not in text, f"{how} nothing renders {remnant}")
    ports = list(native_ports(rendered))
    check(not ports, f"{how} nothing serves or addresses the native API's port 8081, got {ports}")
policies = [d["metadata"]["name"] for d in docs if d["kind"] == "NetworkPolicy" and where(d) == "talgent-sandbox"]
check(policies == ["sandbox-boundary"], f"one sandbox policy bounds every sandbox, got {policies}")

# AGENT_CORE_API_ACCESS: enabled, the entry admits every Principal on the
# core's routes, leaving admission to each procedure's declaration, and
# Agents call the core at the entry's public origin; disabled, it admits
# people and Applications only and Agents get no address for the core.
check("authorization" not in auth["spec"], "by default web-auth admits every Principal")
check(container_env(docs, "controlplane").get("PLATFORM_API_ADDRESS", {}).get("valueFrom")
      == {"configMapKeyRef": {"name": "workbench-environment", "key": "WORKBENCH_ORIGIN"}},
      "by default Agents call the core at the entry's public origin")
without_agents = find(switched, "SecurityPolicy", "web-auth")["spec"].get("authorization")
check(without_agents == {"defaultAction": "Deny", "rules": [{"name": "people-and-applications", "action": "Allow",
      "principal": {"jwt": {"provider": "account", "claims": [{"name": "principal", "values": ["user", "application"]}]}}}]},
      f"AGENT_CORE_API_ACCESS=disabled admits only user and application tokens on the core's routes, got {without_agents}")
check(container_env(switched, "controlplane").get("PLATFORM_API_ADDRESS") == {"name": "PLATFORM_API_ADDRESS", "value": ""},
      f"AGENT_CORE_API_ACCESS=disabled gives Agents no address for the core, got {container_env(switched, 'controlplane').get('PLATFORM_API_ADDRESS')}")

# The same switch decides the platform scope's core API plugin: the
# controlplane's configuration, with its placeholders filled from the
# container's environment, holds talgent-api by default and not when Agents
# have no direct access to the core.
def built_in_platform(rendered):
    config = yaml.safe_load(find(rendered, "ConfigMap", "controlplane-config")["data"]["defaults.yml"])
    env = container_env(rendered, "controlplane")
    def fill(entry):
        def value(match):
            name, _, default = match.group(1).partition(":")
            return env[name].get("value", "") if name in env else default
        return re.sub(r"\$\{(.*?)\}", value, entry)
    return [slug for slug in map(fill, config["agent-built-in-plugins"]["platform"]) if slug.strip()]

check(built_in_platform(docs) == ["talgent", "talgent-api"],
      f"by default every Work loads talgent and talgent-api, got {built_in_platform(docs)}")
check(built_in_platform(switched) == ["talgent"],
      f"AGENT_CORE_API_ACCESS=disabled leaves talgent-api out of the platform scope, got {built_in_platform(switched)}")

# ADDITIONAL_ENTRY_HOST: empty, every route serves WORKBENCH_HOST only; set,
# the web-api, auth and computer routes serve that host too, while the
# Workbench's pages stay on its own.
def hostnames(rendered, route):
    return find(rendered, "HTTPRoute", route)["spec"]["hostnames"]

for route in ("workbench-web", "web-api", "auth", "computer"):
    check(hostnames(docs, route) == ["workbench.example.com"],
          f"by default route {route} serves only WORKBENCH_HOST, got {hostnames(docs, route)}")
for route in ("web-api", "auth", "computer"):
    check(hostnames(switched, route) == ["workbench.example.com", "entry.example.org"],
          f"ADDITIONAL_ENTRY_HOST=entry.example.org adds it to route {route}, got {hostnames(switched, route)}")
check(hostnames(switched, "workbench-web") == ["workbench.example.com"],
      f"ADDITIONAL_ENTRY_HOST leaves route workbench-web on WORKBENCH_HOST, got {hostnames(switched, 'workbench-web')}")

# CONTROLPLANE_PROFILE: empty, controlplane reads its defaults only; set, it
# also reads controlplane_<profile>.yml of ConfigMap controlplane-profile
# beside them. Neither mounts a Secret for the profile: nothing it configures
# reads one.
def pod_spec(rendered, deployment):
    return find(rendered, "Deployment", deployment)["spec"]["template"]["spec"]

def mounted(rendered, deployment, path):
    spec = pod_spec(rendered, deployment)
    mount = next((m for m in spec["containers"][0]["volumeMounts"] if m["mountPath"] == path), None)
    if mount is None:
        return None
    return next(v for v in spec["volumes"] if v["name"] == mount["name"]), mount

check("JIMU_PROFILES_ACTIVE" not in container_env(docs, "controlplane"), "by default controlplane activates no profile")
check(mounted(docs, "controlplane", "/app/configs/controlplane")[0].get("configMap") == {"name": "controlplane-config"},
      "by default controlplane's configuration is ConfigMap controlplane-config alone")
check(mounted(docs, "controlplane", "/etc/talgent/profile") is None, "by default controlplane mounts no profile Secret")
check(container_env(switched, "controlplane").get("JIMU_PROFILES_ACTIVE") == {"name": "JIMU_PROFILES_ACTIVE", "value": "example"},
      f"CONTROLPLANE_PROFILE=example activates profile example, got {container_env(switched, 'controlplane').get('JIMU_PROFILES_ACTIVE')}")
configuration = mounted(switched, "controlplane", "/app/configs/controlplane")[0]
check(configuration.get("projected", {}).get("sources") == [{"configMap": {"name": "controlplane-config"}},
                                                           {"configMap": {"name": "controlplane-profile"}}],
      f"CONTROLPLANE_PROFILE puts ConfigMap controlplane-profile beside controlplane-config, got {configuration}")
volume_names = lambda rendered: [v["name"] for v in pod_spec(rendered, "controlplane")["volumes"]]
check(volume_names(switched) == volume_names(docs),
      f"CONTROLPLANE_PROFILE mounts no volume beside the configuration, such as a Secret for the profile, got {volume_names(switched)}")

if failures:
    print("\n".join(f"expected: {f}" for f in failures), file=sys.stderr)
    sys.exit(1)
PYTHON
echo "k3s-render: ok"
