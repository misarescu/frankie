# 09 - Deployment profiles

The same codebase supports two profiles. Differences are Helm values and driver/connector choices, not forks.

| Concern | Full profile | Laptop profile |
|---|---|---|
| Kubernetes | Full cluster | **k3s, single node** |
| Postgres | One instance, separate DBs for app/Temporal/LiteLLM | Same (shared instance) |
| Temporal | Production deployment | **Single binary on the shared Postgres** |
| Event bus | NATS or Redis Streams | **Postgres LISTEN/NOTIFY, or in-process** |
| LLM gateway | LiteLLM | LiteLLM (kept: keeps API keys out of sandboxes) |
| Registry | Harbor (scanning, proxy cache) or Zot | **Plain `registry:2`** |
| Dependency proxies | Athens/Verdaccio/devpi or Nexus | **Egress to public registries + per-repo cache volumes** |
| Image building | Rootless BuildKit as a service | **One-off Job when a toolchain image changes** |
| Preview environments | One per change request | **At most one at a time, or off** |
| Observability | OTel + Tempo/Prometheus/Loki or Langfuse | **Temporal UI, factory run view, `kubectl logs`; VictoriaMetrics later** |
| Concurrency | Configurable | `maxConcurrentIssues = 1`; reviewers sequential |
| Remote access | Ingress + OIDC | Tailscale (or similar) |

Network allowlist differs accordingly: on the laptop profile, egress to public package registries is permitted in addition to the LLM gateway and git host, because there is no dependency proxy.

## Laptop sizing (16 GiB RAM, 4 cores)

Memory budget, GiB:

| Component | Min | Max | Change from the full design |
|---|---|---|---|
| OS + k3s (single node) | 1.5 | 1.5 | k3s instead of full Kubernetes |
| Postgres | 1.0 | 1.0 | One instance for app, Temporal and LiteLLM |
| Temporal | 0.5 | 1.0 | Single binary on the shared Postgres |
| Control plane API + UI | 0.3 | 0.3 | |
| LiteLLM gateway | 0.5 | 0.5 | Kept: keeps API keys out of sandboxes |
| Flux | 0.3 | 0.3 | |
| Local registry | 0.1 | 0.1 | Plain registry:2 |
| Egress proxy | 0.05 | 0.05 | D20; hostname allowlist for agent pods |
| **Platform total** | **4.25** | **4.75** | |

Available for agents, gates, previews: 16 - 4.75 = **11.25 GiB** (worst case).

Concurrent pods:

| Pod type | Limit each (GiB) | Count at once | Total (GiB) | Notes |
|---|---|---|---|---|
| Coder / test writer | 3 | 2 | 6 | Builders compile and run tests; **CPU is the real limit on 4 cores** |
| Reviewer | 2 | 0 | 0 | Run reviewers one after another, after the builders finish |
| Command gate | 3 | 1 | 3 | Fresh build and tests from the pushed commit |
| **Total** | | **3** | **9** | Fits in 11.25 GiB with 2.25 GiB headroom |

Caveat: the sheet does not account for per-pod **Kata VM overhead** (guest kernel and agent, typically a few hundred MiB per pod) or page cache. Measure it in M1 and adjust limits (open question).

## Dropped or deferred on the laptop

| Dropped or deferred | On the laptop instead |
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
- [ ] **Concurrency settings**: `maxConcurrentIssues = 1`; run reviewers sequentially.

## Profile-driven requirements

- **FR-PR-1** Interfaces (event bus, registry client, dependency proxy config) are abstracted so a profile swaps implementations by Helm value.
- **FR-PR-2** The platform must start and pass the M1 acceptance test on the laptop profile within the memory budget above.
- **FR-PR-3** Resource requests/limits for platform and pod types are Helm values with the laptop numbers as the documented baseline.
