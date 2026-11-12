# Flux manifests for the platform

`clusters/<profile>/` is the path a cluster is bootstrapped from. Flux writes its own
`flux-system/` directory there during bootstrap; do not hand-edit it.

Bootstrap the laptop cluster (needs a GitHub token with repo access in `GITHUB_TOKEN`):

```sh
flux bootstrap github --owner=misarescu --repository=frankie --personal \
  --branch=main --path=deploy/flux/clusters/laptop
```

The HelmRelease renders `deploy/helm/factory` with `values.yaml` plus the profile file.
