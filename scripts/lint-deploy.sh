#!/usr/bin/env bash
# Lints the Helm chart for every profile and schema-validates rendered and Flux manifests.
set -euo pipefail
cd "$(dirname "$0")/.."

chart=deploy/helm/frankie
crd_schemas='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
kc=(kubeconform -strict -summary -schema-location default -schema-location "$crd_schemas")

for profile_values in "$chart"/values-*.yaml; do
	helm lint --strict "$chart" -f "$profile_values" --set images.tag=sha-lint
	helm template frankie "$chart" -f "$profile_values" --set images.tag=sha-lint | "${kc[@]}" -
done

"${kc[@]}" deploy/flux

# Flux builds the chart from git with the source revision as semver build
# metadata (0.1.0+<sha>.1); every label must stay a valid label value then too.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
helm package "$chart" --version 0.1.0+lint.1 -d "$tmp" >/dev/null
if helm template frankie "$tmp/frankie-0.1.0+lint.1.tgz" -f "$chart/values-concrete.yaml" --set images.tag=sha-lint |
	grep -nE '^ +[a-z0-9./-]+: "?[^"]*\+'; then
	echo "lint-deploy: a label value carries '+' when Flux versions the chart with its source revision" >&2
	exit 1
fi
