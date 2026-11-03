# Applications and agents

What customers build on top of their models: workflows, agents, tools and published apps.
This is separate from the product agent that operates ownsolid itself.

- Owns: `services/control/internal/modules/workflows`, `plugins`, `applications`.
- Depends on: gateway, identity-and-access, app-catalog.

## P1 Enterprise release

- [ ] APP-101 Visual workflow editor with free connections (P1) - Catalog: F036
- [ ] APP-102 Node types: start, LLM, condition, loop, parallel, variable, code, HTTP, tool, end (P1) - Catalog: F036, F037
- [ ] APP-103 Human approval node that pauses and resumes (P1) - Catalog: F039
- [ ] APP-104 Agent node with tool-calling loop, limits and stop (P1) - Catalog: F034, F043
- [ ] APP-105 MCP client with a private tool catalog and per-tool permissions (P1) - Catalog: F052, F053
- [ ] APP-106 Prompt templates with versions and comparison (P1) - Catalog: F035
- [ ] APP-107 Run history with node-level traces, inputs, outputs and cost (P1) - Catalog: F044
- [ ] APP-108 Publish an app version as web chat, API and embeddable widget; rollback (P1) - Catalog: F045
- [ ] APP-109 Conversations with history, search, rename and export (P1) - Catalog: F004
- [ ] APP-110 Playground for models and apps (P1) - Catalog: F428
- [ ] APP-111 Sandboxed code execution with time, memory and network limits (P1) - Catalog: F055

## P2 Fleet operations

- [ ] APP-201 Skills: import, version, permissions and loading budgets (P2) - Catalog: F601 to F605
- [ ] APP-202 Memory with source, isolation and deletion (P2) - Catalog: F041, F596
- [ ] APP-203 Multi-agent collaboration with budget trees and cancellation (P2) - Catalog: F042, F597
- [ ] APP-204 Checkpointed long tasks with idempotent external actions (P2) - Catalog: F040, F595
- [ ] APP-205 Approval bound to exact action and version; channels and async tasks (P2) - Catalog: F598, F599

## P4 Breadth

- [ ] APP-401 A2A remote agents (P4) - Catalog: F038
- [ ] APP-402 Plugin types: knowledge (via DeepParser), generation, task API, web search (P4) - Catalog: F056 to F059
- [ ] APP-403 Third-party plugin marketplace with review (P4) - Catalog: F051

Agent framework adapters (LangGraph, AgentScope, Microsoft Agent Framework, AutoGen,
Dify app API) are in the compatibility matrix.
Catalog rows absorbed: F004, F034 to F059, F428, F595 to F605.
