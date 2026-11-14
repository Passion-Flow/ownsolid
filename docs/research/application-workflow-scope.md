# Workflow and application scope

The workflow owner currently admits two bounded graph schemas. The applications
owner projects publication and conversation metadata for the signed-in portal user.
These are useful building blocks, but they do not provide a general graph engine or
a versioned package format.

- [Dify workflow nodes](https://docs.dify.ai/en/use-dify/nodes/human-input) cover
  branching, iteration, agent calls, code, variables, HTTP and human input.
  [Temporal patterns](https://docs.temporal.io/design-patterns) cover durable waits,
  checkpointed tasks, parallel execution and cancellation. These validate the
  direction of APP-101 to APP-107 and APP-111, with signed tools and explicit
  limits in the existing ownsolid execution owner.
- [MCP tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
  describe discovery, input schemas and tool invocation. Discovery is data and
  each operation still needs local admission. The protocol does not confer any
  permission to call an endpoint advertised by an untrusted server.
- [Dify app packaging](https://docs.dify.ai/en/use-dify/workspace/app-management)
  separates moving a version between installations from publishing it to users
  and leaves credentials out of the package. APP-108 covers publishing and
  rollback. APP-207 adds portable packages for a separate offline installation.
- [n8n subworkflows](https://docs.n8n.io/build/flow-logic/break-workflows-into-smaller-parts.md)
  declare child inputs and link parent and child executions. [GitHub Actions reuse](https://docs.github.com/en/actions/reference/workflows-and-actions/reusing-workflow-configurations)
  shows immutable version selection and nested permissions that only narrow.
  APP-206 covers reusable deterministic flows separately from APP-203's agent
  collaboration.

The references are design examples. The review adopts no external dependency,
model or dataset. Qualify actual dependencies, vendor versions and failure paths
in the research note for each item before implementation.
