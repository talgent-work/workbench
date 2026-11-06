#!/usr/bin/env python3
"""Verify the shipped bootstrap with real local NSS; owns only temporary files/processes."""
import argparse
import hashlib
import os
from pathlib import Path
import signal
import ssl
import sqlite3
import subprocess
import tempfile
import time

BASE = "sandbox-registry.cn-zhangjiakou.cr.aliyuncs.com/opensandbox/execd:v1.1.0@sha256:6cf7dba2f21f0b536e100563d841ac58a9f31c2b0a081b7ac76796a24d6f47e2"


def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, timeout=30, **kwargs).stdout


def bootstrap(image):
    source = run("docker", "run", "--rm", "--entrypoint", "cat", image, "/bootstrap.sh").decode()
    start = source.index("trust_mitm_ca_nss() ")
    closing = "\n)\n" if source[start:].startswith("trust_mitm_ca_nss() (") else "\n}\n"
    return source[start:source.index(closing, start) + len(closing)]


def certificate(root, name):
    pem = root / (name + ".pem")
    run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=" + name, "-keyout", str(root / (name + ".key")), "-out", str(pem))
    return pem


def invoke(function, home, certs, timeout=20):
    # Children use the real certutil binary. A stdin barrier releases concurrent
    # bootstraps together; process groups bound only these owned local children.
    processes = [subprocess.Popen(["sh", "-c", function + '\nread -r start\ntrust_mitm_ca_nss "$1"', "nss-check", str(cert)],
                                 env=dict(os.environ, HOME=str(home)), stdin=subprocess.PIPE,
                                 stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                 start_new_session=True) for cert in certs]
    try:
        for process in processes:
            process.stdin.write(b"start\n")
            process.stdin.close()
        deadline = time.monotonic() + timeout
        while any(process.poll() is None for process in processes) and time.monotonic() < deadline:
            time.sleep(0.02)
        return [process.poll() for process in processes]
    finally:
        for process in processes:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
            process.wait()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def key_state(path):
    # NSS adds certificate/trust-attribute HMACs to key4.db metaData. File bytes
    # may legitimately change on a CA import; password and old key rows must not.
    with sqlite3.connect(path.as_uri() + "?mode=ro", uri=True) as database:
        database.execute("PRAGMA query_only=ON")
        metadata = {row[0]: row[1:] for row in database.execute("SELECT * FROM metaData")}
        keys = list(database.execute("SELECT * FROM nssPrivate ORDER BY id"))
    assert "password" in metadata, "fixture must have an initialized password record"
    return metadata, keys


def assert_key_state_preserved(before, path):
    # sig_cert_* rows are NSS's signatures over certificate and trust
    # attributes; NSS 3.120 re-signs existing certificates on an import, so
    # their values may change. The certificates themselves are compared with
    # certutil; the password record, every other metadata row and the private
    # key rows must stay byte-identical.
    old_metadata, old_keys = before
    metadata, keys = key_state(path)
    assert keys == old_keys, "existing private key rows changed"
    assert all(metadata.get(key) == value for key, value in old_metadata.items() if not key.startswith("sig_cert_")), "existing NSS password or metadata changed"
    assert all(key in metadata for key in old_metadata), "existing NSS metadata removed"
    assert all(key.startswith("sig_cert_") for key in metadata.keys() - old_metadata.keys()), "unexpected new key metadata"


def verify(image, prove_before):
    binaries = ["/execd", "/execd.exe", "/execd-ebpf", "/opensandbox-supervisor"]
    original = run("docker", "run", "--rm", "--entrypoint", "sha256sum", BASE, *binaries)
    shipped = run("docker", "run", "--rm", "--entrypoint", "sha256sum", image, *binaries)
    assert original == shipped, "official execd binaries changed"
    function = bootstrap(image)
    with tempfile.TemporaryDirectory(prefix="talgent-nss-check-") as directory:
        root = Path(directory)
        first, second, history = [certificate(root, name) for name in ("sandbox-a", "sandbox-b", "historical")]
        if prove_before:
            before = bootstrap(BASE)
            reproduced = False
            for attempt in range(5):
                home = root / ("before-" + str(attempt))
                home.mkdir()
                codes = invoke(before, home, [first, second] * 4, timeout=3)
                if None in codes:
                    reproduced = True
                    print("RED: official bootstrap concurrent first use left hung NSS children", flush=True)
                    break
            assert reproduced, "original concurrency failure not reproduced in bounded attempts"
        home = root / "shared-home"
        home.mkdir()
        assert invoke(function, home, [first] * 8) == [0] * 8, "shared first-use bootstrap did not finish"
        db = "sql:" + str(home / ".pki/nssdb")
        run("certutil", "-A", "-d", db, "-n", "historical", "-t", "C,,", "-i", str(history))
        history_before = run("certutil", "-L", "-d", db, "-n", "historical", "-a")
        key_before = key_state(home / ".pki/nssdb/key4.db")
        assert invoke(function, home, [first, second] * 4) == [0] * 8, "existing shared DB re-entry failed"
        assert run("certutil", "-L", "-d", db, "-n", "historical", "-a") == history_before
        assert_key_state_preserved(key_before, home / ".pki/nssdb/key4.db")
        assert len(key_state(home / ".pki/nssdb/key4.db")[0]) > len(key_before[0]), "new CA must exercise legitimate signature metadata growth"
        for certificate_file in (first, second):
            nickname = "opensandbox-mitmproxy-" + digest(certificate_file)
            actual = run("certutil", "-L", "-d", db, "-n", nickname, "-a")
            assert ssl.PEM_cert_to_DER_cert(actual.decode()) == ssl.PEM_cert_to_DER_cert(certificate_file.read_text()), "active sandbox CA lost"
        lock = home / ".pki/nssdb/.opensandbox-nss.lock"
        assert not lock.exists(), "successful bootstrap left its lock"
        invalid = root / "invalid.pem"
        invalid.write_text("invalid certificate\n")
        assert invoke(function, home, [invalid], timeout=5) == [0]
        assert not lock.exists(), "failed NSS import left its lock"
        # A different/stale owner must never be removed based on age or local PID.
        lock.mkdir()
        owner = lock / "owner"
        owner.write_text("sandbox=other-sandbox pid=22\n")
        outcome = subprocess.run(["sh", "-c", function + '\ntrust_mitm_ca_nss "$1"', "nss-check", str(first)],
                                 env=dict(os.environ, HOME=str(home)), stdin=subprocess.DEVNULL,
                                 capture_output=True, timeout=20)
        assert outcome.returncode == 0  # Existing best-effort browser trust contract.
        assert b"not initialized: shared NSS lock unavailable" in outcome.stderr
        assert owner.read_text() == "sandbox=other-sandbox pid=22\n", "removed another owner's lock"
        owner.unlink()
        lock.rmdir()
        protected = root / "protected-home"
        protected_db = protected / ".pki/nssdb"
        protected_db.mkdir(parents=True)
        password = root / "password"
        password.write_text("local-regression-password\n")
        password.chmod(0o600)
        db = "sql:" + str(protected_db)
        run("certutil", "-N", "-d", db, "-f", str(password))
        run("certutil", "-A", "-d", db, "-n", "historical", "-t", "C,,", "-i", str(history), "-f", str(password))
        # Populate a private key as well as trust data so accidental key deletion
        # is distinguishable from a harmless certificate signature addition.
        bundle = root / "historical.p12"
        run("openssl", "pkcs12", "-export", "-inkey", str(root / "historical.key"),
            "-in", str(history), "-out", str(bundle), "-passout", "file:" + str(password))
        run("pk12util", "-i", str(bundle), "-d", db, "-k", str(password), "-w", str(password))
        key_before = key_state(protected_db / "key4.db")
        assert key_before[1], "fixture must contain historical private key rows"
        history_before = run("certutil", "-L", "-d", db, "-n", "historical", "-a")
        assert invoke(function, protected, [first], timeout=5) == [0], "password-protected DB must finish noninteractively"
        assert_key_state_preserved(key_before, protected_db / "key4.db")
        assert run("certutil", "-L", "-d", db, "-n", "historical", "-a") == history_before
    print("PASS: concurrent creation/re-entry, both CA entries, historical certificate/password preservation, failure release/bounded foreign-lock wait, official binary hashes", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("image", help="built derived execd-installer image")
    parser.add_argument("--prove-before", action="store_true")
    args = parser.parse_args()
    verify(args.image, args.prove_before)
