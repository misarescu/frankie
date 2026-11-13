# Flux manifests for the platform

`clusters/<profile>/` is the path a cluster is bootstrapped from. Flux writes its own
`flux-system/` directory there during bootstrap; do not hand-edit it.

Bootstrap the homelab cluster (concrete profile) (needs a GitHub token with repo access in `GITHUB_TOKEN`):

```sh
flux bootstrap github --owner=misarescu --repository=frankie --personal \
  --branch=main --path=deploy/flux/clusters/concrete
```

The HelmRelease renders `deploy/helm/frankie` with `values.yaml` plus the profile file.
