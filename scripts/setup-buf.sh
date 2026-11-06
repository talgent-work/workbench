#!/usr/bin/env bash
# Install the Buf CLI. Code generators run as version-pinned remote plugins on the BSR.
set -euo pipefail

BUF_VERSION="v1.67.0"

echo -e "\033[1;36m==> Installing buf CLI ${BUF_VERSION}\033[0m"
go install "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}"
