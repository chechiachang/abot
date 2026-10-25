# Implementation tasks

Source: [plan](../plan.md). Implement files and their sections in order; checkboxes track completed work.

| Order | Task | Depends on |
| --- | --- | --- |
| 01 | [Foundation and Pi adapter](01-foundation.md) | None |
| 02 | [Administration and authorization](02-administration.md) | 01 |
| 03 | [Durable coding runtime](03-runtime.md) | 01–02 |
| 04 | [Telegram bridge and delivery](04-telegram.md) | 01–03 |
| 05 | [MCP and orchestration](05-tools.md) | 02–04 |
| 06 | [Media input](06-media.md) | 03–04 |
| 07 | [URL reading and publishing](07-url-reading.md) | 04–06 |
| 08 | [Domain integrations and images](08-integrations.md) | 02–07 |
| 09 | [Observability and releases](09-production.md) | 01–08 |

Keep packages small; introduce interfaces only at real boundaries. No web UI. Never commit credentials.

Each task includes acceptance checks. Record verified setup/run/format/lint/test/build commands in `AGENTS.md` when established; unresolved commands and external APIs stay TODO. Update the plan after each completed capability. Optional integrations must not prevent the core bot from starting when disabled.
