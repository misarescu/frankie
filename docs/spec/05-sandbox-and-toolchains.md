# 05 - Sandbox and toolchains

Safe unattended running depends on this document. Treat every statement as a requirement.

## Sandbox

- **FR-SB-1** One Kubernetes pod per agent step (and per `command` gate), using a Kata Containers runtime class. No host mounts. Non-root user.
- **FR-SB-2** Egress allowlist (D20). `NetworkPolicy` lets agent pods reach only cluster DNS, the LLM gateway, the control plane (git proxy) and the egress proxy; everything else is dropped. The egress proxy allows HTTPS `CONNECT` only to listed hostnames: in-cluster registry/dependency proxy (full profile) or public package registries (concrete profile). It refuses plain HTTP, IP literals that are not listed, and any destination resolving to a loopback, link-local, private or other non-public address, so an allowlisted name cannot be pointed at the LAN or a metadata service. Pods get `HTTPS_PROXY`, and `NO_PROXY` for cluster-internal names. A project adds plain hostnames to the list in `.factory/config.yaml` (`egress: {allow: [...]}`, D80), read from the base branch when a run starts; the run's agent, gate and build pods then carry a short-lived signed egress token as the password in their proxy URL, and the proxy allows those hosts on port 443 for a pod with a valid token. A deployment can refuse project additions (`egressProxy.projectAllow: false`): a run of a project that adds hosts then fails with the reason.
- **FR-SB-3** Git credentials (D21): the pod gets a short-lived token for the control plane's git proxy, never a git provider credential. The token is bound to one repo and one issue; the proxy allows reading the repo and updating refs only under `refs/heads/agent/<issue-key>/`. Tokens are stateless HMAC-signed grants (repo, issue key, step, expiry), so they survive control plane restarts and cannot be revoked early: issue them with a TTL of the step timeout plus a small margin. The proxy strips the pod's credentials and adds the provider credential; a refused push is reported per ref through git's own protocol (`[remote rejected] ... agents may only push refs/heads/agent/<issue-key>/*`), branch deletions and tags are refused, and a push with any denied ref is rejected as a whole. An upstream authentication failure is returned as 502, never as a challenge the pod could answer.
- **FR-SB-4** Model credentials: a short-lived LiteLLM virtual key with budget. Real provider keys never reach the pod.
- **FR-SB-5** Agents never receive `kubectl` or any cluster credentials. ServiceAccount for agent pods has no RBAC bindings (`automountServiceAccountToken: false`).
- **FR-SB-6** Workspace handoff between steps is the git branch. Each step clones, works, commits, pushes. A step starts from the issue's agent branch if it exists (an earlier step or Run pushed it), else from the base branch (D36). Steps are stateless and reproducible, and git history is the audit log. A PVC per issue is a possible faster, stateful alternative later.
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

Implementation (D44): the platform protects, for every project, `.factory/**` (workflows, roles, project config, the protected-paths list), the toolchain files (`mise.toml`, `mise.lock`, `.mise.toml`, `.mise/**`), CI configuration (`.github/workflows/**`, `.github/actions/**`, `.gitlab-ci.yml`, `.circleci/**`) and lint configuration (`.golangci.*`, ESLint and Biome configs, `.pre-commit-config.yaml`) (`config.DefaultProtectedPaths`). A repo adds its own guardrails in `.factory/protected-paths.yaml` (`paths:`, globs where `**` spans directories); it cannot remove a default. The list is read from the base branch into the run's snapshot. When a run's steps have passed, `DetectProtectedChanges` asks the provider for the diff from the run's base commit to its last push (`internal/guard`): a protected path changed (or renamed away), a test file deleted or renamed to a non-test file, a skip marker added in a test file (Go `Skip`, `//go:build ignore`, pending Ginkgo specs, `it.skip`/`xit`, pytest and unittest skips, `@Disabled`/`@Ignore`), fewer test declarations than before, a test file whose diff the provider left out, or a diff too large to list in full each force the `protected-changes` human gate. Detection errs towards asking: a skip call inside a comment still counts.

## Trust model for results

Agents run build and tests inside their own loop so most problems are fixed before a gate. Two rules keep this safe:

1. **Agent results are never trusted.** `command` gates re-run everything from the pushed commit in a fresh pod. "Tests pass" from an agent is a hint, not evidence.
2. **Guardrail files are protected** (above).

## Toolchains and build environments

Approach: **platform base image + toolchain declared in the repo**. Rejected alternatives: one fat image with every tool (huge, cannot match each repo's pins), hand-maintained per-language images (version drift, combinatorial maintenance), toolchain in a sidecar (harness shell cannot reach it easily).

1. **Platform base image** (`factory-base:vX`), versioned by the platform: runner, Claude Code, opencode, pi (with Node/Bun), git, non-root user, ripgrep, jq, make, curl, mise, and a C toolchain (gcc, libc6-dev) for cgo (D34).
2. **Toolchain declared in the repo with `mise.toml`**: pins languages and CLI tools (`aqua` backend for verified prebuilt binaries) and defines tasks. Humans, CI and agents share the same file. Gates call `mise run test` / `mise run lint`, which keeps workflow definitions generic across repos.
3. **Images built by the factory.** The control plane hashes the base image reference + `mise.toml`/`mise.lock` (or the Dockerfile; the Dockerfile path is deferred, D30). Since D75 the toolchain image is hashed and built from `mise.toml` without its `tasks` and `task_config` tables, which installing tools never reads, so editing a task does not rebuild it; a dependency image still includes the whole `mise.toml`, since its warm-up runs a task. A missing image is built in-cluster by appending one layer to the image it extends, without BuildKit (D81): a Job named after the image's tag runs the install in the base image itself as the agent user, packs what it created or changed, appends that to the base image (mirrored into the in-cluster registry once, by digest) and pushes the result. Requests for one image meet in that Job and see its outcome; a failed build is kept until its TTL, and a request that starts after it failed builds again (D78). A failed build's error, and so the run's transcript, ends with the last lines of its log (D79). Every agent step and gate for a given commit uses the same image. The registry is not trusted (D45): builds push by digest only, the control plane reads the digest the build's push container reported through the kubelet, checks the registry holds that manifest, records it in Postgres (`toolchain_images`) and only then tags it; pods run toolchain images by recorded digest, so a tag overwritten by another build pod changes nothing. The registry keeps what the records need: every 24 hours, when no build runs, the control plane deletes the images beyond the newest few per repository and the base mirrors of runner images no longer deployed, forgets their records, and restarts the registry, which collects its garbage before it serves again (D84). A Renovate-style job opens toolchain-bump issues for agents.
4. **Dependency images (M3.1).** A repo may declare its dependencies in `.factory/config.yaml`:

   ```yaml
   dependencies:
     files: [go.mod, go.sum, web/package.json, web/bun.lock]   # globs from the repo root
     run: mise run deps:warm                                    # downloads, and pre-compiles where it can
   ```

   The control plane builds a thin image `FROM` the toolchain image (by digest) containing only those files at their paths, runs the command as the agent user with the sandbox environment (proxy, `GOPROXY`, ...), and records the result by digest under a hash of the toolchain digest and the files' contents. Agent and gate pods run the dependency image. The mechanism is language-neutral: the repo's task is what knows `go mod download`, `mvn dependency:go-offline`, `uv sync --no-install-project`, `dotnet restore` or `conan install`, and each tool caches where it always does under the agent's home, which the image layer captures. A pre-compile step that needs the list of imported packages (Go) can read a list the repo generates and declares among the files. The repo's own code is never in the image, so it changes only when dependencies do. Agent steps read the files from the commit they start from, gates from the commit under test, like the toolchain (D32, D33).

   Implementation (D47): the files are the repo's paths at that commit matching the globs (GitHub's tree listing), at most 200 files and 512 KiB, plus `mise.toml` and `mise.lock` always, since mise resolves the warm-up's tools from them. The image is built like a toolchain image (D81): the files travel as a tar archive in the ConfigMap (which keeps directory paths), the warm-up runs in the toolchain image itself, pulled by digest, and its layer is appended to that same digest in the in-cluster registry; it lives in the registry repository `dependencies/<repo>` and is recorded in `toolchain_images` like any built image. The warm-up runs in `/workspace/deps`, which the pods' workspace volume hides at run time. A repo whose declaration matches no file, or that declares dependencies without a `mise.toml`, fails the agent step before any pod; a commit under test that breaks its declaration fails its gate, so a loop back can fix it.
4. **Escape hatch**: `.factory/Dockerfile` starting `FROM factory-base:vX` for system libraries or unusual setups.
5. **Dependency proxy** instead of open internet egress: Athens for Go modules (every profile, D37), Verdaccio for npm and devpi for pip (full profile), or one Nexus covering all. Faster installs, supply-chain checkpoint, no direct internet from agent pods. Sandboxes reach the proxy through `GOPROXY` and a NetworkPolicy rule; the checksum database stays allowlisted.
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
