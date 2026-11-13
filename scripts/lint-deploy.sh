#!/usr/bin/env bash
# Lints the Helm chart for every profile and schema-validates rendered and Flux manifests.
set -euo pipefail
cd "$(dirname "$0")/.."

chart=deploy/helm/factory
crd_schemas='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
kc=(kubeconform -strict -summary -schema-location default -schema-location "$crd_schemas")

for profile_values in "$chart"/values-*.yaml; do
	helm lint --strict "$chart" -f "$profile_values" --set images.tag=sha-lint
	helm template factory "$chart" -f "$profile_values" --set images.tag=sha-lint | "${kc[@]}" -
done

"${kc[@]}" deploy/flux
