#!/usr/bin/env bash
# Checks that this machine can develop Frankie. FAIL lines block development;
# WARN lines only block the features named on that line. Read-only: changes nothing.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

failures=0
ok() { printf 'OK    %s\n' "$*"; }
warn() { printf 'WARN  %s\n' "$*"; }
fail() {
	printf 'FAIL  %s\n' "$*"
	failures=$((failures + 1))
}

echo "== Tools pinned in mise.toml"
if ! command -v mise >/dev/null 2>&1; then
	fail "mise not installed (brew install mise)"
else
	missing=$(mise ls --current --missing 2>/dev/null)
	if [ -n "$missing" ]; then
		fail "tools not installed, run: mise install"
		printf '%s\n' "$missing" | sed 's/^/        /'
	else
		ok "all tools pinned in mise.toml are installed ($(mise ls --current | wc -l | tr -d ' ') tools)"
	fi
fi
if command -v git >/dev/null 2>&1; then ok "git"; else fail "git not installed"; fi

echo "== Container runtime"
if docker info >/dev/null 2>&1; then
	ok "docker daemon reachable"
else
	warn "docker daemon not reachable: needed by 'mise run db:diff' (Atlas dev database)"
fi

echo "== Cluster (laptop profile)"
if ! command -v kubectl >/dev/null 2>&1; then
	warn "kubectl not on PATH; skipping cluster checks"
elif ! context=$(kubectl config current-context 2>/dev/null); then
	warn "no kubeconfig context: cluster features unavailable"
elif ! kubectl --request-timeout=10s get --raw /readyz >/dev/null 2>&1; then
	warn "cluster '$context' not reachable"
else
	ok "cluster '$context' reachable ($(kubectl --request-timeout=10s get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ') node(s))"
	if kubectl --request-timeout=10s get namespace flux-system >/dev/null 2>&1; then
		ok "flux installed"
	else
		warn "flux not installed: bootstrap per deploy/flux/README.md"
	fi
	if kubectl --request-timeout=10s get runtimeclass --no-headers 2>/dev/null | awk '{print $2}' | grep -q '^kata'; then
		ok "kata runtime class present"
	else
		warn "no kata runtime class: agent pods cannot be sandboxed yet (needed from M1, docs/spec/05)"
	fi
fi

echo
if [ "$failures" -gt 0 ]; then
	echo "doctor: $failures blocking problem(s)"
	exit 1
fi
echo "doctor: ready for development"
