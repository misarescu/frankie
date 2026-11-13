# 05 - Sandbox and toolchains

Safe unattended running depends on this document. Treat every statement as a requirement.

## Sandbox

- **FR-SB-1** One Kubernetes pod per agent step (and per `command` gate), using a Kata Containers runtime class. No host mounts. Non-root user.
- **FR-SB-2** Egress allowlist (D20). `NetworkPolicy` lets agent pods reach only cluster DNS, the LLM gateway, the control plane (git proxy) and the egress proxy; everything else is dropped. The egress proxy allows HTTPS `CONNECT` only to listed hostnames: in-cluster registry/dependency proxy (full profile) or public package registries (concrete profile). It refuses plain HTTP, IP literals that are not listed, and any destination resolving to a loopback, link-local, private or other non-public address, so an allowlisted name cannot be pointed at the LAN or a metadata service. Pods get `HTTPS_PROXY`, and `NO_PROXY` for cluster-internal names.
- **FR-SB-3** Git credentials (D21): the pod gets a short-lived token for the control plane's git proxy, never a git provider credential. The token is bound to one repo and one issue; the proxy allows reading the repo and updating refs only under `refs/heads/agent/<issue-key>/`. Tokens are stateless HMAC-signed grants (repo, issue key, step, expiry), so they survive control plane restarts and cannot be revoked early: issue them with a TTL of the step timeout plus a small margin. The proxy strips the pod's credentials and adds the provider credential; a refused push is reported per ref through git's own protocol (`[remote rejected] ... agents may only push refs/heads/agent/<issue-key>/*`), branch deletions and tags are refused, and a push with any denied ref is rejected as a whole. An upstream authentication failure is returned as 502, never as a challenge the pod could answer.
- **FR-SB-4** Model credentials: a short-lived LiteLLM virtual key with budget. Real provider keys never reach the pod.
- **FR-SB-5** Agents never receive `kubectl` or any cluster credentials. ServiceAccount for agent pods has no RBAC bindings (`automountServiceAccountToken: false`).
- **FR-SB-6** Workspace handoff between steps is the git branch. Each step clones, works, commits, pushes. Steps are stateless and reproducible, and git history is the audit log. A PVC per issue is a possible faster, stateful alternative later.
- **FR-SB-7** Resource requests/limits set per pod type (see `09-deployment-profiles.md`). Ephemeral storage limits set.
- **FR-SB-8** Pods are labeled with run and step IDs, have an `activeDeadlineSeconds`, and are garbage collected on completion or failure.
- **FR-SB-9** Integration-test services (e.g. Postgres) run as sidecar containers in the same pod. **No Docker inside agent pods.**
- **Implementation**: agent pods run in their own namespace (`frankie-agents`) with a deny-all NetworkPolicy plus explicit allows, and Pod Security Admission `restricted` enforced, so the API server refuses a privileged or root pod even if something other than the pod builder (`internal/sandbox`) creates it. The egress proxy only accepts agent pods and may itself only reach public addresses on 443 (`deploy/helm/frankie/templates/`). `e2e/sandbox_test.go` (Ginkgo, D25) checks all of this from a real Kata pod.
- **Prerequisite**: Kata needs hardware virtualization. If the Kubernetes nodes are VMs, nested virtualization must be enabled. On bare metal/laptop: VT-x/AMD-V enabled and `/dev/kvm` present.

Optional stronger runtime classes (gVisor, Kata with different VMMs) are selectable per step via `runtimeClassName` in project config.

## Protected paths

Changes to these paths, and skipped/deleted tests, force a **human gate** even if the workflow does not include one. The reviewer roles are told to flag them.

```
lint configs, CI configs, mise.toml / mise.lock / .factory/Dockerfile,
deploy/**/networkpolicy*, RBAC manifests, Kata runtime class config,
cmd/runner/**, internal/runner/**, proto/frankie/runner/**,
gate definitions, LiteLLM budget config,
the protected-paths list itself (.factory/protected-paths.yaml)
```

Detection runs in the control plane against the diff (not in the agent pod). Test removal/skip detection looks for deleted test files, newly added skip markers (`t.Skip`, `it.skip`, `xit`, etc.) and a lower test count.

## Trust model for results

Agents run build and tests inside their own loop so most problems are fixed before a gate. Two rules keep this safe:

1. **Agent results are never trusted.** `command` gates re-run everything from the pushed commit in a fresh pod. "Tests pass" from an agent is a hint, not evidence.
2. **Guardrail files are protected** (above).

## Toolchains and build environments

Approach: **platform base image + toolchain declared in the repo**. Rejected alternatives: one fat image with every tool (huge, cannot match each repo's pins), hand-maintained per-language images (version drift, combinatorial maintenance), toolchain in a sidecar (harness shell cannot reach it easily).

1. **Platform base image** (`factory-base:vX`), versioned by the platform: runner, Claude Code, opencode, pi (with Node/Bun), git, non-root user, ripgrep, jq, make, curl, mise, and a C toolchain (gcc, libc6-dev) for cgo (D34).
2. **Toolchain declared in the repo with `mise.toml`**: pins languages and CLI tools (`aqua` backend for verified prebuilt binaries) and defines tasks. Humans, CI and agents share the same file. Gates call `mise run test` / `mise run lint`, which keeps workflow definitions generic across repos.
3. **Images built by the factory.** The control plane hashes the base image reference + `mise.toml`/`mise.lock` (or the Dockerfile; the Dockerfile path is deferred, D30). A missing image is built in-cluster with BuildKit and pushed to the in-cluster registry. Every agent step and gate for a given commit uses the same image. A Renovate-style job opens toolchain-bump issues for agents.
4. **Escape hatch**: `.factory/Dockerfile` starting `FROM factory-base:vX` for system libraries or unusual setups.
5. **Dependency proxy** instead of open internet egress (full profile): Athens for Go modules, Verdaccio for npm, devpi for pip, or one Nexus covering all. Faster installs, supply-chain checkpoint, no direct internet from agent pods.
6. **Services for integration tests** declared in the repo: `.factory/config.yaml` with `services: [postgres:17]`, run as sidecars (D35). An entry is an image reference or an object `{name, image, port, env}`; well-known images (`postgres`, `redis`) get a port, a non-root user, a data directory and defaults from a catalog, so the shorthand works under Pod Security `restricted`. Services run as native sidecars of agent and gate pods and are reached on `localhost`; when a port is known, the step starts only once it accepts connections. The platform sets how many services a pod may have and each one's limits, which come out of the pod's own limits.

   ```yaml
   # .factory/config.yaml
   services:
     - postgres:17            # localhost:5432, user postgres, trust auth
     - name: cache
       image: redis:7.4
   ```
7. **Language servers** (gopls etc.) installed via `mise.toml`. Harnesses with LSP integration (opencode) use them; others get equivalent feedback from `go build`/`go vet` through the shell.

Example `mise.toml`:

```toml
[tools]
go = "1.25"
node = "22"
python = "3.12"
"aqua:golangci/golangci-lint" = "2.4"
"aqua:bufbuild/buf" = "1.57"
"go:golang.org/x/tools/gopls" = "latest"
"pipx:yamllint" = "1.37"

[tasks.test]
run = "go test ./..."
[tasks.lint]
run = ["golangci-lint run", "buf lint", "yamllint ."]
```

Concrete profile replaces items 3-5 with simpler mechanisms; see `09-deployment-profiles.md`.
