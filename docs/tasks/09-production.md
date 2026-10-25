# 09. Production observability and releases

Depends on: 01–08 for a full release; earlier core-only releases must declare disabled/unimplemented integrations.

## 1. Observability

- [ ] Add stable request/chat/generation/delivery correlation IDs to redacted logs without logging message bodies or secrets by default.
- [ ] Add optional Logfire tracing for requests, URL extraction, tools, token usage, retries, compaction, and delivery. SDK/exporter and transfer settings: TODO.
- [ ] Keep tracing disabled when unconfigured; exporter outages must not prevent agent execution or delivery.

## 2. Deployment

- [ ] Build a cached, pinned multi-stage image with the Go binary and required Pi/Node/media/browser dependencies only.
- [ ] Add Docker Compose with persistent session/artifact state and coding workdir, non-root runtime, configuration/secret mounts, and health checks. No web UI service.
- [ ] Define readiness for policy/provider/runtime initialization and liveness without triggering model calls.
- [ ] Document minimal setup/run/update/backup/restore commands and filesystem permissions; verify graceful shutdown and volume ownership.

## 3. Release

- [ ] Add versioned Go releases and container publication. Registry, deployment target, tagging, and release commands: TODO.
- [ ] Use Changesets/npm publication only for separately published Pi/Node packages; omit it otherwise.
- [ ] Establish release checks and rollback/restore steps before deployment; publish/deploy only when requested.

## Acceptance checks

- [ ] Run end-to-end Telegram coding, permission denial, cancellation, reply forks, compaction, and restart/outbox recovery in a disposable environment.
- [ ] Verify media/image delivery and enabled URL/domain integrations; explicitly report disabled features.
- [ ] Verify log/trace redaction, non-root operation, health checks, persistent-state backup/restore, and failed-exporter behavior.
- [ ] Run established format/lint/test/build checks and replace verified workflow command TODOs. End-to-end command: TODO after harness exists.
