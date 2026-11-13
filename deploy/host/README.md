# Host configuration

Settings the Kubernetes nodes need beyond a stock k3s install. Apply them on
every node (and after rebuilding one); `mise run doctor` checks the cluster.

## AppArmor signal fix (D28)

Ubuntu 26.04 sets `kernel.apparmor_restrict_unprivileged_unconfined=1`. With it,
runc stacks containerd's AppArmor profile (`cri-containerd.apparmor.d//&unconfined`)
instead of switching to it, and containerd's default profile then denies every
signal between a container's own processes ([containerd#12886],
[containerd#14485]). Symptoms: Postgres crash-loops ("could not signal for
checkpoint"), Prisma hangs (LiteLLM never becomes ready), Go programs lose
SIGURG preemption. Agent pods are unaffected because they run in Kata VMs.

```sh
sudo install -m 0644 deploy/host/sysctl.d/99-frankie-apparmor.conf /etc/sysctl.d/
sudo sysctl -p /etc/sysctl.d/99-frankie-apparmor.conf
# restart pods started before the change; they keep the stacked label
```

Containers stay confined by `cri-containerd.apparmor.d (enforce)`. The trade-off
is one relaxed Ubuntu hardening: unprivileged unconfined processes may change
AppArmor profiles again (`apparmor_restrict_unprivileged_userns` stays on).
Remove the file once k3s ships a containerd with the template fix.

[containerd#12886]: https://github.com/containerd/containerd/issues/12886
[containerd#14485]: https://github.com/containerd/containerd/issues/14485
