# shellcheck shell=bash
# The Talgent Computer release a platform release deploys. publish-npm.yml
# publishes @talgent/computer at a release only when its package changed, so
# a platform release X.Y.Z runs the newest @talgent/computer npm has at or
# below X.Y.Z. TALGENT_COMPUTER_RELEASE (X.Y.Z) names it instead, for an
# offline deployment; TALGENT_NPM_REGISTRY names another registry.
#
# A release npm has accepted but not yet shown is not found: the render then
# names the previous Computer release, and renders the new one once npm shows
# it.

computer_release() {
  local release="$1" metadata version chosen=""
  if [[ -n "${TALGENT_COMPUTER_RELEASE:-}" ]]; then
    if ! [[ "${TALGENT_COMPUTER_RELEASE}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      echo "TALGENT_COMPUTER_RELEASE must be X.Y.Z, got ${TALGENT_COMPUTER_RELEASE}" >&2
      return 1
    fi
    echo "${TALGENT_COMPUTER_RELEASE}"
    return
  fi
  if ! metadata="$(curl -fsSL --max-time 30 -H 'Accept: application/vnd.npm.install-v1+json' \
    "${TALGENT_NPM_REGISTRY:-https://registry.npmjs.org}/@talgent%2fcomputer")"; then
    echo "could not read @talgent/computer's releases from npm; set TALGENT_COMPUTER_RELEASE to the Talgent Computer release to deploy" >&2
    return 1
  fi
  # Each published version is a key of the metadata's versions object.
  while read -r version; do
    [[ -n "${version}" ]] || continue
    if [[ "$(printf '%s\n%s\n' "${version}" "${release}" | sort -V | head -n1)" == "${version}" ]]; then
      chosen="${version}"
    fi
  done < <(grep -oE '"[0-9]+\.[0-9]+\.[0-9]+":\{' <<< "${metadata}" | tr -d '"{:' | sort -V)
  if [[ -z "${chosen}" ]]; then
    echo "npm has no @talgent/computer at or below ${release}; set TALGENT_COMPUTER_RELEASE" >&2
    return 1
  fi
  echo "Talgent Computer ${chosen} for platform release ${release}" >&2
  echo "${chosen}"
}
