# 05. Tool orchestration and MCP

Depends on: 02–04.

## 1. MCP connections

- [ ] Define administrator-owned stdio/HTTP connection configuration, credential references, startup/shutdown, timeouts, and retry limits.
- [ ] Discover capabilities and expose only globally allowed tools further filtered by effective chat policy. Use stable names to avoid collisions.
- [ ] Keep connection management inaccessible to model input; redact credentials and server failures in diagnostics.
- [ ] Add CLI/config examples for Chrome DevTools and Firecrawl without embedded secrets. Exact transports/package versions: TODO.
- [ ] Handle disconnected servers, changed capabilities, cancellation, and subprocess cleanup explicitly.

## 2. Codemode

- [ ] Verify Pi's native JavaScript Codemode API and sandbox guarantees: TODO.
- [ ] Expose only permission-filtered host tools; check authorization on every dispatched call, including nested orchestration.
- [ ] Enforce a host-owned deadline, bounded output, and cancellation across the entire orchestration.
- [ ] Persist execution/progress through the durable runtime. Do not describe Codemode as a sandbox for `bash` or other coding tools.

## Acceptance checks

- [ ] Denied MCP tools cannot be called directly or through Codemode.
- [ ] A hanging server/script terminates at the host deadline and releases resources.
- [ ] Stdio/HTTP lifecycle errors are actionable and do not expose credentials.
