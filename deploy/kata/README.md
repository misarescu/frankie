# Kata Containers

Agent pods and `command` gates run under the `kata` RuntimeClass (docs/spec/05-sandbox-and-toolchains.md).
Installed with kata-deploy until Flux manages it:

```sh
helm upgrade --install kata-deploy \
  oci://ghcr.io/kata-containers/kata-deploy-charts/kata-deploy --version 4.3.1 \
  --namespace kube-system -f deploy/kata/values.yaml --wait --timeout 15m
```

Job mode only installs on nodes present at install/upgrade time: re-run the command after adding a node.
