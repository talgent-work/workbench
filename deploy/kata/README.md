# Reference sandbox runtime: Kata Containers on k3s

Sandboxes need a RuntimeClass that runs each Pod in its own VM. This directory is the bundled way
to provide one on k3s: Kata Containers 4.1.0, RuntimeClass `kata-qemu-runtime-rs` (the QEMU
runtime-rs, Kata 4.x's default). A cluster that already has a VM-isolating runtime uses it
instead and sets `TALGENT_SANDBOX_RUNTIME_CLASS` ([self-hosting](../../docs/self-hosting.md#2-prerequisites)).

## Install

`scripts/deploy/install-kata.sh` installs the kata-deploy chart, checksum-verified, with
`values.yaml` and its node installer image pinned by digest, on nodes labeled
`talgent.io/kata-install=true`, one at a time. It restarts each node's container runtime, so drain
running sandboxes first. It needs `helm`, `kubectl`, `curl` and `sha256sum`.

Check each node before labeling it:

1. **Virtualization.** VMX or SVM is enabled, `/dev/kvm` exists, and the KVM API version is 12.
2. **containerd drop-ins.** k3s's containerd config imports the Kata drop-in directory. With a
   custom `config-v3.toml.tmpl`, merge this top-level entry into it and restart k3s:
   `imports = ["/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.d/*.toml"]`.
   The installer refuses a config that cannot load its drop-ins.
3. **Encrypted pod network** (the reference network, verified by `apply-opensandbox.sh`): k3s
   server option `flannel-backend: wireguard-native` with the network policy controller on; every
   node then reports `flannel.alpha.coreos.com/backend-type=wireguard` and a public key in
   `flannel.alpha.coreos.com/backend-data`. Nodes need kernel WireGuard support.

Done when `kubectl get runtimeclass kata-qemu-runtime-rs` succeeds and a sandbox Pod reaches
`Running`.

## Tuning

`values.yaml` runs virtiofsd with 16 request threads instead of 1: with one thread, parallel file
writes on a JuiceFS mount (each close waits for its object upload) run serially, so checkouts slow
down several times over. The override replaces the whole upstream argument list; recheck it
against runtime-rs's `DEFVIRTIOFSEXTRAARGS` when the chart version changes. Re-running the
installer applies a change; only sandboxes started afterwards get it.

The RuntimeClass's Pod overhead (250m CPU, 320Mi) is part of controlplane's `computer.cloud.demand`;
keep the two in step.

Sources: [Kata Helm chart](https://kata-containers.github.io/kata-containers/installation/#helm-chart),
[Kata 4.1.0](https://github.com/kata-containers/kata-containers/releases/tag/4.1.0),
[k3s networking](https://docs.k3s.io/networking/basic-network-options).
