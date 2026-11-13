# 11 - Open questions and spec inconsistencies

## Open questions

1. **Kata memory overhead.** The concrete profile sizing does not include per-pod VM overhead or page cache. First measurement (2026-11-13, kata-deploy 4.3.1, `qemu-runtime-rs`, idle busybox pod): ~280 MiB host RSS per pod (QEMU 243, shim 27, virtiofsd 12). The `kata` RuntimeClass declares `overhead.podFixed` of 320Mi / 250m, so the scheduler accounts for it: 3 concurrent pods cost ~1 GiB, cutting the laptop headroom from 2.3 to ~1.3 GiB. Still open: overhead under real build load and page cache; revise `09-deployment-profiles.md` once measured in M1.
2. ~~**Nested virtualization.** Required if Kubernetes nodes are VMs. Confirm the target host is bare metal (the laptop) or that nesting is enabled.~~ Resolved: the laptop host is bare metal (i5-7300HQ, VT-x enabled, `/dev/kvm` present, Ubuntu 26.04); Kata runs there (`deploy/kata/`).
3. **In-cluster registry (full profile).** Harbor (heavier; scanning, proxy cache) vs Zot (lightweight). The concrete profile uses `registry:2`.
4. **Dependency proxy (full profile).** One Nexus, or one proxy per ecosystem (Athens, Verdaccio, devpi).
5. **Preview limits.** How many concurrent previews the homelab can hold, and their TTL.
6. **ACP adapter maturity.** Whether to rely on ACP for Claude Code/pi or on their native protocols.
7. **Auth.** Single-user token vs OIDC. Needed before exposing the UI beyond Tailscale.
8. **Secrets management.** SOPS vs external-secrets for the GitOps repo.
9. ~~**Migration tool.** golang-migrate vs goose. Decide in M0.~~ Resolved by D19 (Atlas authors, goose applies).
10. **Spec approval placement.** This spec puts spec approval in the issue lifecycle (`refining` > `ready`) before a workflow starts, rather than as a workflow step. Confirm, or move it into the default workflow as a `human` gate.
11. **Merge approval default.** Decision D10 is auto-merge once gates pass. An earlier note listed PR merge as a human gate "initially". Confirm that auto-merge is the default and whether a per-project `human` gate before `enqueue` should be recommended for the first weeks.
12. **Object storage for transcripts.** MinIO, S3-compatible service, or PVC directory (concrete profile).
13. ~~**Branch naming and issue ID format** (`agent/<issue-id>/<slug>`) - finalize with the GitHub token scoping rule.~~ Resolved by D22 (issue keys like `frk-42`, branches `agent/frk-42/<slug>`) and D21 (branch scoping enforced by the git proxy).
14. **AppArmor blocks Postgres on the homelab host.** On Ubuntu 26.04 with k3s's containerd, the `cri-containerd.apparmor.d` profile denies signals between a container's own processes once they carry a stacked label (`apparmor="DENIED" operation="signal" ... peer="cri-containerd.apparmor.d//&unconfined"`), so Postgres cannot signal its checkpointer and crash-loops. Workaround: `postgres.appArmorUnconfined: true` in the concrete profile (Postgres only; agent pods run in Kata and are unaffected). Revisit when k3s or Ubuntu update the profile, then remove the workaround.

## Inconsistencies found while consolidating the source notes (resolved here)

| Topic | Earlier notes | Resolution in these specs |
|---|---|---|
| E2E before merge | Test-agent notes: e2e cannot run before merge without ephemeral environments; build loop only checks tests compile. | Design doc D14: preview per change request from the start, so e2e gates merges. Concrete profile limits previews to 1 (or off, in which case e2e runs on staging as in the earlier notes). |
| Example workflow shape | Sequential `implement` then `e2e-tests`. | Both steps in a `parallel` group (D9 says parallel). |
| Merge approval | Exploration note: human gate for PR merge initially. | D10: auto-merge. See open question 11. |
| Sandbox workspace | Exploration note: one Job per agent run; design: one pod per agent step with git handoff. | Per step, git branch handoff (`05-sandbox-and-toolchains.md`). |
| Event bus | Design: NATS/Redis Streams. Laptop sizing: drop NATS. | Profile-dependent (`09-deployment-profiles.md`). |
| Dependency proxy | Design: proxy instead of internet egress. Laptop sizing: public egress + cache volumes. | Profile-dependent. |
| Orchestration | Exploration note: "Go + Postgres, or Temporal". | Temporal chosen (D7); Postgres for everything else (D13). |
| Self-hosting milestone | Build-order step 3 in the source. | Milestone M3 here (milestones are renumbered from M0 to allow the exploration/scaffold step). |
