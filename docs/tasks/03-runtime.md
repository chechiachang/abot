# 03. Durable agent and coding runtime

Depends on: 01–02. Gate: durable state must be usable independently of Telegram.

## 1. Conversations and recovery

- [x] Implement a SQLite-backed ADK session service using the ownership contract from 01. Define schema and migrations in Go; use the pure-Go SQLite driver.
- [x] Persist conversation entries, branch checkpoints, generation/tool task status, and terminal results. A partial unique index limits each chat/branch to one active turn.
- [x] Add bounded Azure retries, archived summary-based compaction, prioritized steering/follow-up queues, and context cancellation around the ADK runner.
- [x] On restart, mark unfinished generations and tool records uncertain, requeue unprocessed inputs, and recover pending Telegram delivery. Interrupted side-effecting tools are never replayed.
- [x] Preserve durable IDs and archived history across compaction and checkpoint forks; persist failed/cancelled turn status.

## 2. Coding tools and skills

- [x] Enable `read`, `bash`, `edit`, and `write` only through the effective policy; recheck authorization at execution.
- [x] Configure coding workdir, command deadlines, output limits, and process cleanup. Document actual OS/filesystem access; a workdir is not a sandbox.
- [x] Discover Agent Skills from configured locations; apply global/default/per-chat selection and keep metadata distinct from tool permissions.
- [x] Validate missing/invalid skills with actionable diagnostics. Use Agent Skills frontmatter and document discovery paths.

## 3. Progress and inspection

- [x] Persist structured steps with pending/in-progress/completed/blocked status and branch/generation IDs.
- [x] Restore checkpoint progress on branch forks and preserve task-attributed history through compaction.
- [x] Add read-only CLI inspection of branches, entries, generation/tool tasks, records, queued inputs, and redacted raw details. Inspection never starts an agent.
- [x] Import historical chat JSONL with dry-run validation, format version/source metadata, duplicate handling, import reports, and imported/skipped/failed counts.

## Acceptance checks

- [x] Restart during a generation restores inspectable state without replaying a shell/file side effect.
- [x] Cancellation, retry exhaustion, compaction, steering, and follow-ups preserve coherent task state.
- [x] Two chats remain isolated; conflicting turns cannot corrupt branch history.
- [x] Denied tools stay denied when selected by a skill or requested directly.
- [x] A disposable repository coding task edits files and runs checks within documented permissions.
