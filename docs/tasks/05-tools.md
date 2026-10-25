# 05. Tool orchestration and MCP

Depends on: 02–04.

## 1. MCP connections

- [x] Define administrator-owned stdio/HTTP connection configuration, environment-variable credential references, startup/shutdown, timeouts, and bounded retry limits.
- [x] Discover capabilities per turn and expose only globally allowed connections further filtered by effective chat policy. Use bounded, stable names and reject collisions.
- [x] Keep connection management inaccessible to model input; do not emit credential values or MCP server error bodies in diagnostics.
- [x] Add `mcp list`, `mcp validate`, and `mcp probe` plus Chrome DevTools stdio and Firecrawl HTTP examples without embedded secrets.
- [x] Handle disconnected servers, changed capabilities on the next turn, cancellation, and subprocess cleanup explicitly.

## 2. ADK tool orchestration

- [x] Delegate coding work through Go/ADK sub-agents and expose only policy-filtered coding tools to the coding agent; MCP tools are coordinator-only.
- [x] Recheck authorization on every coding/MCP tool dispatch, including delegated coding calls.
- [x] Enforce host-owned deadlines, bounded MCP output, and cancellation across the agent/tool workflow.
- [x] Persist execution/progress through the durable runtime; document actual process filesystem permissions because a workdir is not a sandbox.

## Acceptance checks

- [x] Denied MCP tools cannot be called directly; delegated agents do not receive MCP tools.
- [x] A hanging tool call returns at the host deadline; closing the MCP toolset closes its HTTP session or stdio process.
- [x] Stdio/HTTP lifecycle errors are actionable and do not expose credentials.

Run `abot mcp validate` to check configuration, `abot mcp list` to inspect global/default selections, and `abot mcp probe <connection>` to discover tool names. HTTP credentials and stdio environment values are referenced by environment-variable name only.
