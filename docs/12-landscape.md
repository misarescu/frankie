# 12 - Landscape

Exploration notes from November 2026. The industry term is **software factory** (also: agent orchestration, agentic SDLC, background/async coding agents). Mastra defines it as a system that uses AI agents and automated processes to manage software development tasks. Naming collision to watch when searching: **Factory** is also a coding company.

The distinction that matters: having an agent that can write code vs having a system that reliably produces software.

## What already exists (worth hands-on testing before building)

- **Vibe Kanban** (Apache-2.0, self-hosted): kanban board that dispatches Claude Code, Codex, Gemini CLI and others into isolated git worktrees, then lets you review and merge diffs from one UI. Closest to the board idea at single-developer scale. Starting point to improve on.
- **Gas Town / Gas City** (Steve Yegge): "Kubernetes for AI coding agents". Gas City is the MIT-licensed platform. Key idea: the platform is generic and your factory is the configuration it runs.
- **AgentFactory** (renseiai): orchestrates a fleet of coding agents (Claude, Codex, A2A-compatible) through a development > QA > acceptance pipeline, with Linear integration. Closest to the role pipeline.
- **OpenHands**: Software Agent SDK covering agents, tools, conversations, workspaces and events. An execution layer, not a full factory.
- **Warp Factories** (commercial): useful mental model of stages - triage, spec, implementation, review, verification, shipping, monitoring.

Most run agents in git worktrees on the host. None combines Kubernetes-native sandboxing with a GitOps release flow, which is where this project adds the most value.

## Sources

- How to Build an Open Source Software Factory - Eden AI: https://www.edenai.co/post/how-to-build-an-open-source-software-factory
- 9 Best Software Factory Platforms and Tools (2026) - Mastra: https://mastra.ai/articles/best-software-factory
- AI Software Factory - Gas City: https://gascity.com/guide/ai-software-factory/
- The Software Factory - alexop.dev: https://alexop.dev/posts/the-software-factory/
- Best Software Factory Platforms in 2026 - Warp: https://www.warp.dev/articles/best-software-factory-platforms-2026
- AgentFactory - GitHub: https://github.com/renseiai/agentfactory
- Vibe Kanban - GitHub: https://github.com/BloopAI/vibe-kanban
- opencode server docs: https://opencode.ai/en/docs/server
- opencode ACP support: https://opencode.ai/en/docs/acp
- pi RPC mode docs: https://cdn.jsdelivr.net/npm/@mariozechner/pi-coding-agent/docs/rpc.md
- ACP adapter for Claude Code and pi (Go): https://github.com/beyond5959/acp-adapter
- Agent Client Protocol: https://agentclientprotocol.com/overview/clients
- Ephemeral Environments for GitHub Pull Requests - Flux Operator: https://fluxoperator.dev/docs/resourcesets/github-pull-requests/
- Pull Request Generator in ApplicationSets - Argo CD guide: https://oneuptime.com/blog/post/2026-02-26-argocd-applicationset-pull-request-generator/view
- mise aqua backend: https://mise.jdx.dev/dev-tools/backends/aqua.html
