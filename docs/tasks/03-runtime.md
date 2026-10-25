# 03. Durable agent and coding runtime

Depends on: 01–02. Gate: durable state must be usable independently of Telegram.

## 1. Conversations and recovery

- [ ] Integrate Pi Durable per-chat SQLite storage using the ownership contract from 01. Exact package/API and schema/migration responsibilities: TODO.
- [ ] Persist conversation entries, branch checkpoints, generation/tool task status, and terminal results. Define a single active-turn policy per chat/branch and serialize conflicting updates.
- [ ] Implement bounded transient retries, compaction, steering, queued follow-ups, and cancellation using supported Pi APIs.
- [ ] On restart, reconcile unfinished tasks with persisted state. Do not automatically rerun interrupted side-effecting tools; expose uncertain outcomes for inspection.
- [ ] Preserve durable IDs and branch context across compaction; recover cleanly from failed/cancelled turns.

## 2. Coding tools and skills

- [ ] Enable `read`, `bash`, `edit`, and `write` only through the effective policy; recheck authorization at execution.
- [ ] Configure coding workdir, command deadlines, output limits, and process cleanup. Document actual OS/filesystem access; a workdir is not a sandbox.
- [ ] Discover Agent Skills from configured locations; apply global/default/per-chat selection and keep metadata distinct from tool permissions.
- [ ] Validate missing/invalid skills with actionable diagnostics. Discovery API and skill format: TODO after Pi selection.

## 3. Progress and inspection

- [ ] Persist structured steps with pending/in-progress/completed/blocked status and branch/generation IDs.
- [ ] Restore the correct progress after branch switching and compaction; keep events attributable to their original task.
- [ ] Add read-only CLI inspection of branches, entries, generation/tool tasks, records, and raw details. Redact secrets and do not trigger agent execution.

## Acceptance checks

- [ ] Restart during a generation restores inspectable state without replaying a shell/file side effect.
- [ ] Cancellation, retry exhaustion, compaction, steering, and follow-ups preserve coherent task state.
- [ ] Two chats remain isolated; conflicting turns cannot corrupt branch history.
- [ ] Denied tools stay denied when selected by a skill or requested directly.
- [ ] A disposable repository coding task edits files and runs checks within documented permissions.
