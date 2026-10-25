# 09. Production observability and releases

Depends on: 01–08 for a full release; earlier core-only releases must declare disabled/unimplemented integrations.

## 1. Observability

- [x] Add stable request/chat/generation/delivery correlation IDs to redacted trace spans without logging message bodies or secrets by default.
- [x] Add optional Logfire OTLP HTTP tracing for requests, URL extraction, tools, token usage, retries, compaction, and delivery. Configure `LOGFIRE_TOKEN` through the environment; default endpoint is `https://logfire-api.pydantic.dev/v1/traces`.
- [x] Keep tracing disabled when unconfigured; batch exporter outages do not prevent agent execution or delivery.

## 2. Deployment

- [x] Build a cached multi-stage image pinned to Go 1.26.5 with required media and Chromium dependencies.
- [x] Add Docker Compose with persistent session/artifact state and coding workdir, non-root runtime, configuration/secret mounts, and readiness health check. No web UI service.
- [x] Define `abot healthcheck` readiness for policy/provider/runtime path and instructions without model calls; Docker process state provides liveness.
- [x] Document setup/run/backup/restore commands and UID 10001 filesystem requirements; Bridge handles graceful signal shutdown.

## 3. Release

- [x] Add versioned Go releases and GHCR container publication on `v*` tags; no deployment target is configured.
- [x] Document rollback via prior image/tag and restore from the SQLite backup procedure; release workflow only runs on pushed tags.

## Acceptance checks

- [ ] Run end-to-end Telegram coding, permission denial, cancellation, reply forks, compaction, and restart/outbox recovery in a disposable environment.
- [ ] Verify media/image delivery and enabled URL/domain integrations; explicitly report disabled features.
- [ ] Verify full log/trace redaction, non-root operation, backup/restore, and exporter-outage behavior in Docker integration runs.
- [x] Run established format/lint/test/build checks; end-to-end Telegram command remains TODO until a disposable Bot API harness exists.
