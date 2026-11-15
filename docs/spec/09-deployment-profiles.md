# 09 - Deployment profiles

The same codebase supports two profiles: **full** and **concrete** (called "laptop" before D24; it is sized for a single 16 GiB laptop host). Differences are Helm values and driver/connector choices, not forks.

| Concern | Full profile | Concrete profile |
|---|---|---|
| Kubernetes | Full cluster | **k3s, single node** |
| Postgres | One instance, separate DBs for app/Temporal/LiteLLM | Same (shared instance) |
| Temporal | Production deployment on Postgres | **Single binary (`temporal server start-dev`) on SQLite, D29** |
| Event bus | NATS or Redis Streams | **Postgres LISTEN/NOTIFY, or in-process** |
| LLM gateway | LiteLLM | LiteLLM (kept: keeps API keys out of sandboxes) |
| Registry | Harbor (scanning, proxy cache) or Zot | **Plain `registry:2`** for toolchain images; platform images come from GHCR (D23) |
| Dependency proxies | Athens/Verdaccio/devpi or Nexus | **Egress to public registries + per-repo cache volumes** |
| Image building | BuildKit as a service | **One-off Job when a toolchain image changes**, appending a layer without BuildKit (D81) |
| Preview environments | One per change request | **At most one at a time, or off** |
| Observability | OTel + Tempo/Prometheus/Loki or Langfuse | **Temporal UI, factory run view, `kubectl logs`; VictoriaMetrics later** |
| Concurrency | Configurable | `maxConcurrentIssues = 1`; reviewers sequential |
| Remote access | Ingress + OIDC | Tailscale (or similar) |

Network allowlist differs accordingly: on the concrete profile, egress to public package registries is permitted in addition to the LLM gateway and git host, because there is no dependency proxy.

## Concrete profile sizing (one laptop: 16 GiB RAM, 4 cores)

Memory budget, GiB:

| Component | Min | Max | Change from the full design |
|---|---|---|---|
| OS + k3s (single node) | 1.5 | 1.5 | k3s instead of full Kubernetes |
| Postgres | 1.0 | 1.0 | One instance for app, Temporal and LiteLLM |
| Temporal | 0.15 | 0.5 | Single binary on SQLite (D29) |
| Control plane API + UI | 0.3 | 0.3 | |
| LiteLLM gateway | 0.5 | 0.5 | Kept: keeps API keys out of sandboxes |
| Flux | 0.3 | 0.3 | |
| Local registry | 0.1 | 0.5 | Plain registry:2; 128 MiB was OOM-killed receiving a dependency image (M5.2); collected daily (D84) |
| Egress proxy | 0.05 | 0.05 | D20; hostname allowlist for agent pods |
| Go module proxy (Athens) | 0.06 | 0.25 | D37; Go modules for sandboxes |
| **Platform total** | **4.31** | **5.4** | |

Available for agents, gates, previews: 16 - 5.4 = **10.6 GiB** (worst case).

Concurrent pods:

| Pod type | Limit each (GiB) | CPU request / limit | Count at once | Total (GiB) | Notes |
|---|---|---|---|---|---|
| Coder / test writer | 3 | 0.5 / 2 | 2 | 6 | Builders compile and run tests; **CPU is the real limit on 4 cores** |
| Reviewer, refiner | 3 | 0.25 / 2 | 0 | 0 | Run reviewers one after another, after the builders finish; same limits as a coder |
| Command gate | 3 | 1 / 2 | 1 | 3 | Fresh build and tests from the pushed commit |
| Image build | 4 | 0.25 / 2 | 0 | 0 | Only when a toolchain or dependency image changes; it takes a coder's place while the coder waits for it |
| **Total** | | | **3** | **9** | Fits in 10.6 GiB with 1.6 GiB headroom |

Memory is requested at its limit; CPU is requested below it (D77), from the M5.1 samples (`docs/notes/m5.1-resource-samples.csv`): agents used a median 0.86 CPU (p90 1.81) and up to 2.6 GiB, reviewers under 0.2 CPU and 0.3 GiB, gates a median 1.81 CPU and under 1 GiB, builds a median 0.15 CPU and up to 2.87 GiB. With requests equal to limits, pods waited `Pending` on a node using 2.5 of its 4 CPUs.

Sidecar services (FR-SB-9) do not add to these numbers: their limits are taken out of their pod's limit (D35). The concrete profile allows one service per pod at 0.5 GiB, leaving 2.5 GiB for the step.

Caveat: the sheet does not account for per-pod **Kata VM overhead** (guest kernel and agent, typically a few hundred MiB per pod) or page cache. Measure it in M1 and adjust limits (open question).

## On a cluster with an autoscaled node pool

The concrete profile's sizing assumes one fixed node. On a managed cluster whose agent pool scales with demand (Scaleway Kapsule since M7), three settings keep the autoscaler from undoing work (D99): sandbox and build pods are never evicted when a node is removed (always set); `platform.nodeSelector` keeps the platform on a small fixed pool, which also carries the cluster's add-ons, while `agents.nodeSelector` keeps agent, gate and build pods on the autoscaled pool, so it can scale to zero (D100); and `persistence.storageClassName` picks a class that keeps volumes when their claims are deleted. The pool's nodes need `/dev/kvm` for Kata, and kata-deploy runs as a DaemonSet so nodes the autoscaler adds get Kata too.

## Dropped or deferred in the concrete profile

| Dropped or deferred | In the concrete profile instead |
|---|---|
| NATS | Stream events through Postgres LISTEN/NOTIFY, or in-process |
| Observability stack | Temporal UI, the factory's run view and kubectl logs; a small VictoriaMetrics later for metrics |
| Dependency proxies / Nexus | Egress to public registries, plus per-repo cache volumes |
| Separate BuildKit service | Build toolchain images rarely, as a one-off Job |
| Preview environments | At most one at a time, or off until later |
| Harbor / Zot registry | Plain registry:2 |

## Laptop host checklist

- [ ] **Virtualization for Kata**: enable VT-x / AMD-V in the BIOS and check that `/dev/kvm` exists.
- [ ] **Sleep and power**: no sleep on lid close; power profile always on; the battery acts as a UPS.
- [ ] **Disk**: SSD with at least 200 GB free.
- [ ] **Image garbage collection**: set kubelet image GC thresholds so the disk does not fill.
- [ ] **Thermals**: keep it ventilated; do not give one agent all 4 cores.
- [ ] **Remote access**: Tailscale or similar, to watch agents and approve gates from elsewhere.
- [ ] **AppArmor signal fix** on Ubuntu 26.04+: install `deploy/host/sysctl.d/99-frankie-apparmor.conf` (D28, `deploy/host/README.md`).
- [ ] **Concurrency settings**: `maxConcurrentIssues = 1`; run reviewers sequentially.

## Profile-driven requirements

- **FR-PR-1** Interfaces (event bus, registry client, dependency proxy config) are abstracted so a profile swaps implementations by Helm value.
- **FR-PR-2** The platform must start and pass the M1 acceptance test on the concrete profile within the memory budget above.
- **FR-PR-3** Resource requests/limits for platform and pod types are Helm values with the concrete-profile numbers as the documented baseline.
