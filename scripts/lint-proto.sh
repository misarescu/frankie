#!/usr/bin/env bash
# Lints and format-checks protobuf contracts; skipped until proto/ has files.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=scripts/lib.sh
. scripts/lib.sh

if ! has_files proto '*.proto'; then
	echo "lint-proto: no .proto files under proto/, skipping"
	exit 0
fi
buf lint
buf format --diff --exit-code
