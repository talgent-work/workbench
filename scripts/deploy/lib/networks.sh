# Sourced by render.sh after lib/environment.sh. The deployment's environment
# declares the enterprise networks a sandbox reaches, ENTERPRISE_NETWORKS
# (whitespace-separated CIDR:PORT[,PORT...]), and the cluster's own ranges,
# CLUSTER_NETWORKS (whitespace-separated CIDRs, the k3s pod and Service
# networks unless set). controlplane reads the same two values into the
# sandbox's egress policy (configs/controlplane/defaults.yml); the render
# mirrors them into NetworkPolicy sandbox-enterprise-networks and refuses what
# controlplane refuses (execution.ParseSandboxNetworks).

DEFAULT_CLUSTER_NETWORKS="10.42.0.0/16 10.43.0.0/16"
PRIVATE_NETWORKS="10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10"

# optional_environment_value KEY DEFAULT FILE... prints KEY's value in the
# last FILE that sets it, or DEFAULT when none does.
optional_environment_value() {
  local key="$1" default="$2"
  shift 2
  if grep -qh "^${key}=" "$@"; then
    environment_value "${key}" "$@"
  else
    printf '%s\n' "${default}"
  fi
}

# ipv4_network CIDR prints the network's first address and prefix length as
# integers, failing unless CIDR is IPv4 with its host bits clear.
ipv4_network() {
  local cidr="$1" a b c d bits address
  if [[ ! "${cidr}" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})/([0-9]{1,2})$ ]]; then
    return 1
  fi
  a=$((10#${BASH_REMATCH[1]})) b=$((10#${BASH_REMATCH[2]})) c=$((10#${BASH_REMATCH[3]})) d=$((10#${BASH_REMATCH[4]}))
  bits=$((10#${BASH_REMATCH[5]}))
  if ((a > 255 || b > 255 || c > 255 || d > 255 || bits > 32)); then
    return 1
  fi
  address=$(((a << 24) | (b << 16) | (c << 8) | d))
  if ((bits < 32 && (address & ((1 << (32 - bits)) - 1)) != 0)); then
    return 1
  fi
  printf '%s %s\n' "${address}" "${bits}"
}

# network_contains OUTER INNER succeeds when the network OUTER holds every
# address of INNER.
network_contains() {
  local outer_address outer_bits inner_address inner_bits
  read -r outer_address outer_bits < <(ipv4_network "$1")
  read -r inner_address inner_bits < <(ipv4_network "$2")
  ((outer_bits <= inner_bits)) || return 1
  ((outer_bits == 0)) && return 0
  (((outer_address >> (32 - outer_bits)) == (inner_address >> (32 - outer_bits))))
}

# enterprise_network_component DIR FILE... writes, when the environment FILE...
# declares enterprise networks, the component DIR/enterprise-networks that
# opens them in NetworkPolicy sandbox-enterprise-networks on their TCP ports,
# each but the cluster's own ranges inside it, and prints its name.
enterprise_network_component() {
  local dir="$1" enterprise cluster entry cidr ports port range private egress="" except
  local -a port_list
  shift
  enterprise="$(optional_environment_value ENTERPRISE_NETWORKS "" "$@")" || return 1
  cluster="$(optional_environment_value CLUSTER_NETWORKS "${DEFAULT_CLUSTER_NETWORKS}" "$@")" || return 1
  for range in ${cluster}; do
    if ! ipv4_network "${range}" > /dev/null; then
      echo "CLUSTER_NETWORKS: ${range} is not an IPv4 network in CIDR notation with its host bits clear" >&2
      return 1
    fi
  done
  [[ -n "${enterprise//[[:space:]]/}" ]] || return 0
  for entry in ${enterprise}; do
    cidr="${entry%%:*}"
    ports="${entry#*:}"
    if [[ "${entry}" != *:* ]] || ! ipv4_network "${cidr}" > /dev/null; then
      echo "ENTERPRISE_NETWORKS: ${entry} is not CIDR:PORT[,PORT...] with an IPv4 network whose host bits are clear" >&2
      return 1
    fi
    private=0
    for range in ${PRIVATE_NETWORKS}; do
      network_contains "${range}" "${cidr}" && private=1
    done
    if [[ "${private}" == "0" ]]; then
      echo "ENTERPRISE_NETWORKS: ${cidr} lies outside the private ranges" >&2
      return 1
    fi
    except=""
    for range in ${cluster}; do
      if network_contains "${range}" "${cidr}"; then
        echo "ENTERPRISE_NETWORKS: ${cidr} lies within the cluster's own range ${range}" >&2
        return 1
      fi
      if network_contains "${cidr}" "${range}"; then
        except+="                    - ${range}"$'\n'
      fi
    done
    egress+="          - to:"$'\n'"              - ipBlock:"$'\n'"                  cidr: ${cidr}"$'\n'
    if [[ -n "${except}" ]]; then
      egress+="                  except:"$'\n'"${except}"
    fi
    egress+="            ports:"$'\n'
    IFS=, read -r -a port_list <<< "${ports}"
    if [[ "${ports}" == "" || "${ports}" == *, ]]; then
      port_list+=("")
    fi
    for port in "${port_list[@]}"; do
      if [[ ! "${port}" =~ ^[0-9]{1,5}$ ]] || ((10#${port} < 1 || 10#${port} > 65535)); then
        echo "ENTERPRISE_NETWORKS: ${entry} has an invalid port '${port}'" >&2
        return 1
      fi
      egress+="              - protocol: TCP"$'\n'"                port: $((10#${port}))"$'\n'
    done
  done
  mkdir -p "${dir}/enterprise-networks"
  {
    echo "apiVersion: kustomize.config.k8s.io/v1alpha1"
    echo "kind: Component"
    echo "patches:"
    echo "  - target:"
    echo "      kind: NetworkPolicy"
    echo "      name: sandbox-enterprise-networks"
    echo "    patch: |-"
    echo "      - op: replace"
    echo "        path: /spec/egress"
    echo "        value:"
    printf '%s' "${egress}"
  } > "${dir}/enterprise-networks/kustomization.yaml"
  printf '%s\n' enterprise-networks
}
