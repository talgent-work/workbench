# Kata on K3s

The accepted runtime is `kata-qemu-runtime-rs`, installed by the official 4.1.0
chart. The installer verifies the chart checksum and pins the node installer
image digest. QEMU Rust is the upstream 4.x default; the Go runtime is deprecated.
Only nodes labeled `talgent.io/kata-install=true` are selected, sequentially.
Installation restarts the node's container runtime. Plan this against live work.

Before labeling a node:

1. Verify VMX/SVM, `/dev/kvm`, KVM API version 12, available disk/memory and the
   existing K3s/containerd configuration. A working KVM ioctl is not a VM test.
2. Preserve any customized K3s `config-v3.toml.tmpl`. It must import the Kata
   drop-in directory. The current K3s default data directory requires the
   top-level TOML entry `imports = ["/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/*.toml"]`.
   Merge this with existing imports; do not replace custom runtime definitions.
   Restart K3s and verify the rendered configuration imports this directory.
   The upstream installer refuses to continue if the rendered config cannot
   load its drop-ins. This was observed on the current customized K3s node.
3. Enable `flannel-backend: wireguard-native` in the K3s server configuration,
   retain the network policy controller, restart the affected nodes, and verify
   every node advertises `flannel.alpha.coreos.com/backend-type=wireguard` with a public key in `backend-data`.
   Check actual WireGuard interfaces/peers and cross-node encrypted traffic
   before sending production Vault materials. A management token does not
   encrypt transport. All nodes need kernel WireGuard support.
4. Label only the verified nodes and run `scripts/deploy/install-kata.sh`.
   The script intentionally does not silently edit host configuration.

Use the workload image's non-root user and the upstream root init/egress containers.
Keep fsGroup 1000, shared HOME, Workspace and Artifact semantics. Capacity
experiments are outside the accepted scope.

`values.yaml` overrides one upstream default: virtiofsd runs with 16 request threads
instead of 1. A single thread forwards guest file operations one at a time, and
on JuiceFS every close waits for its object upload, so parallel writers in a
sandbox ran serially: a 2602-file checkout with 8 git workers took 277 s with one
thread and 113 s with 16, matching a runc pod on the same mount. The drop-in replaces the whole
upstream argument list, so recheck it against the runtime-rs default
(`DEFVIRTIOFSEXTRAARGS`) when the chart version changes. Applying it reruns
`scripts/deploy/install-kata.sh`, which restarts each selected node's container
runtime; only sandboxes started afterwards get the new virtiofsd arguments.

Deploy the complete matching image set with `scripts/deploy/apply-opensandbox.sh`.
That command refuses a missing Kata RuntimeClass or non-WireGuard node backend,
installs the sandbox NetworkPolicy, and uses official Server v0.2.2 and egress v1.1.7 images pinned by digest. The controlled execd installer derives from the pinned official v1.1.0 image and changes only its shared-HOME NSS bootstrap; see `deploy/execd/Dockerfile`.
Telemetry uses the `otel-collector:4318` ClusterIP Service in front of the
in-cluster Collector Pods (`deploy/k3s/workbench/otel-collector.yaml`).
The deployment renders the assigned ClusterIP into NetworkPolicy. Agent
provisioning excludes only that ServiceIP from nft denies, never the node IP.
CNI permits only port 4318 on the Service and its post-DNAT backend; nft blocks
direct node traffic before host DNAT. Verify actual Service delivery and direct
node port denials together.
DNS and native API service exceptions follow the same two-layer policy check.

Upstream egress exports the public MITM certificate and upstream execd bootstrap
builds `/opt/opensandbox/merged-ca-certificates.pem`. The shared runtime directory
uses the upstream writable mount; the workload's fsGroup permits bundle creation.
Bootstrap configures Node/Python trust; the sandbox image points Git/curl at the
same merged bundle. CA private keys and Vault sockets stay in the sidecar.

There are no local CA, permission or management-port patches. The egress API uses
upstream port 18080 with its independent authentication. Read-only public CA,
CA-specific fail-closed startup, CA persistence across sidecar restarts, reserved
port protection and immediate request-level Vault revocation are not promised.
Execd uses the upstream PTY protocol without a recovery patch; see
the deployment runbook's native process recovery.

Sources: [Kata chart](https://kata-containers.github.io/kata-containers/installation/#helm-chart),
[Kata 4.1.0](https://github.com/kata-containers/kata-containers/releases/tag/4.1.0),
[K3s networking](https://docs.k3s.io/networking/basic-network-options).
