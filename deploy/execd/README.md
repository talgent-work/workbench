# Shared-HOME NSS bootstrap correction

This installer derives from the exact official execd v1.1.0 digest in `Dockerfile`. It preserves all official binaries. Image construction checks the original bootstrap hash, replaces only `trust_mitm_ca_nss`, then checks the complete resulting script hash. A changed upstream script requires a deliberate review and new hashes.

The shared employee HOME is retained. Cross-Kata `flock` did not exclude a contender on the deployed JuiceFS mount; atomic `mkdir` did. The function therefore acquires a shared directory lock, rechecks the NSS database inside it, and runs noninteractive, bounded NSS commands. Each CA uses its certificate hash as the nickname; other Session and historical entries are retained.

Browser CA setup remains best effort: a failed command or 15-second lock wait emits a warning and does not claim trust initialization succeeded. Ordinary exits release the owned lock. A hard termination can leave `.pki/nssdb/.opensandbox-nss.lock`; it is never stolen based on age or PID. The `owner` file records sandbox hostname and bootstrap PID only for investigation. Confirm through the actual sandbox lifecycle that the recorded owner is gone before removing that exact directory's `owner` file and then the empty directory. A PID in a different VM does not establish ownership or death.

CA entries remain for HOME lifetime. Future cleanup must use actual sandbox/execution lifecycle authority; this patch does not guess which historical trust entries are safe to remove.

Build and verify with installed Docker, Python 3, OpenSSL and `certutil`:

```sh
docker build -f deploy/execd/Dockerfile -t talgent-execd:nss-check .
python3 deploy/execd/verify-nss-bootstrap.py talgent-execd:nss-check --prove-before
```

The check reads the actual image bootstrap, exercises concurrent real NSS creation and reuse, preserves historical certificates and passwords, checks two active CAs coexist, checks failure releases and bounded foreign-lock waiting, and compares all four official execd binary hashes. `--prove-before` additionally reproduces the original race using the official image's function; only temporary local files/processes are involved.

GitHub image selection accepts `execd` and publishes it through the existing image matrix. Deployment uses the checked-in image reference or an explicit already-published `TALGENT_EXECD_IMAGE` tag/digest; product-only `TALGENT_IMAGE_TAG` changes do not switch bootstrap versions. Updating the image causes an OpenSandbox Server rollout and affects future sandboxes; it does not rewrite a running bootstrap process.
