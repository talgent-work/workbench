trust_mitm_ca_nss() (
	cert="$1"
	[ -f "$cert" ] || return 0
	[ -n "${HOME:-}" ] && [ -d "$HOME" ] || return 0
	for tool in certutil timeout sha256sum; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			echo "warning: $tool unavailable; could not initialize browser CA trust" >&2
			return 0
		fi
	done
	pki="${HOME}/.pki/nssdb"
	mkdir -p "$pki" || return 0
	# flock is not mutually exclusive across the deployed Kata/virtiofs clients.
	# Shared filesystem mkdir is atomic across those clients.
	lock="$pki/.opensandbox-nss.lock"
	waited=0
	until mkdir "$lock" 2>/dev/null; do
		if [ "$waited" -ge 15 ]; then
			echo "warning: browser CA trust not initialized: shared NSS lock unavailable" >&2
			return 0
		fi
		sleep 1
		waited=$((waited + 1))
	done
	password_file=""
	trap 'rm -f "$password_file" "$lock/owner"; rmdir "$lock"' EXIT
	trap 'exit 143' TERM
	trap 'exit 130' INT
	# ponytail: a hard-killed owner can leave this directory behind. Remove it only
	# after its recorded sandbox/process is proven gone; never steal by age or PID.
	printf 'sandbox=%s pid=%s\n' "${HOSTNAME:-unknown}" "$$" > "$lock/owner"
	password_file="$(mktemp)" || return 0
	printf '\n' > "$password_file"
	# Recheck only after acquiring the shared lock. Repeated certutil -N on an
	# initialized DB enters password change, even when --empty-password is set.
	if [ -f "$pki/cert9.db" ]; then
		nssdb="sql:$pki"
	elif [ -f "$pki/cert8.db" ]; then
		nssdb="dbm:$pki"
	else
		nssdb="sql:$pki"
		if ! timeout -k 1 10 certutil -N -d "$nssdb" --empty-password -f "$password_file" -@ "$password_file" </dev/null; then
			echo "warning: failed to initialize browser CA trust database" >&2
			return 0
		fi
	fi
	fingerprint="$(sha256sum "$cert")" || return 0
	fingerprint="${fingerprint%% *}"
	# ponytail: CA entries accumulate with HOME lifetime; reclaim only when the
	# owning sandbox lifecycle proves no retained execution still needs the CA.
	nick="opensandbox-mitmproxy-$fingerprint"
	# Never delete another Session's CA or reset an existing database password.
	if ! timeout -k 1 10 certutil -A -d "$nssdb" -n "$nick" -t "C,," -i "$cert" -f "$password_file" </dev/null; then
		echo "warning: failed to import browser CA trust; existing NSS entries retained" >&2
	fi
)
