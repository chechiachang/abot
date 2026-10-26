# Implementation tasks

This directory contains the ordered implementation checklists for the [project plan](../plan.md). Follow dependencies before starting a task; task files define scope and acceptance checks.

| Order | Task | Depends on |
| --- | --- | --- |
| 01 | [Go/ADK foundation and coding agent](01-foundation.md) | None |
| 02 | [Administration and authorization](02-administration.md) | 01 |
| 03 | [Durable coding runtime](03-runtime.md) | 01–02 |
| 04 | [Telegram bridge and delivery](04-telegram.md) | 01–03 |
| 05 | [MCP and orchestration](05-tools.md) | 02–04 |
| 06 | [Media input](06-media.md) | 03–04 |
| 07 | [URL reading and publishing](07-url-reading.md) | 04–06 |
| 08 | [Domain integrations and images](08-integrations.md) | 02–07 |
| 09 | [Observability and releases](09-production.md) | 01–08 |

When completing a task, update its checklist and the plan, and record verified project commands in [`AGENTS.md`](../../AGENTS.md). Keep packages small, optional integrations non-blocking, and credentials out of version control.
