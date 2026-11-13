#!/usr/bin/env bash
# Regenerates all committed generated code. A generator is skipped until it has inputs.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=scripts/lib.sh
. scripts/lib.sh

if has_files proto '*.proto'; then
	buf generate
else
	echo "generate: no .proto files under proto/, skipping buf"
fi

if has_files internal/store/queries '*.sql'; then
	sqlc generate
else
	echo "generate: no queries under internal/store/queries/, skipping sqlc"
fi

# The packages the dependency image pre-compiles (M3.1).
scripts/go-packages.sh
