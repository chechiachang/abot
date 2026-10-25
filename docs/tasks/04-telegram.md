# 04. Telegram bridge and reliable delivery

Depends on: 01–03. Gate: bridge routing/delivery must remain separate from agent execution.

## 1. Inbound routing and commands

- [x] Configure the bot token and administrator notification destination without logging secrets. Use a small Go HTTP adapter for the Telegram Bot API; tokens are environment-only.
- [x] Enforce `telegram.allowed_user_ids` using sender IDs. Private agent requests require a listed sender; group requests also require `telegram.default_group_id`. Other groups and missing/anonymous senders cannot start turns or add context.
- [x] Deduplicate update IDs durably and advance the stored polling offset only after recording each update.
- [x] Route authorized private messages and default-group mentions/replies to the runtime. Store unaddressed messages from allowed users in that group as passive context without starting a turn; discard other group context.
- [x] Implement `/start`, `/help`, `/id`, `/cancel`, `/reset`, and `/steer`; reset selects a fresh durable branch while retaining earlier history and audit records.
- [x] Add `/allow` requests and local `abot access list|approve|reject` commands. Approval records the decision but does not bypass `telegram.allowed_user_ids` or the single-group limit.
- [x] Retry transient polling failures with bounded backoff, honor `retry_after`, and stop/report fatal token or competing-poller errors.
- [x] Send configured startup/shutdown administrator notifications and handle graceful shutdown.

## 2. Rendering and delivery

- [x] Escape Telegram HTML and emit only supported formatting. Publish output above 1,000 Unicode characters through Morsel rather than splitting messages.
- [x] Configure environment-only Morsel credentials and a publish endpoint. POST `{"title":"...","content":"..."}` with a Bearer key and require a public HTTPS `{"url":"..."}` response. Without both settings, long output gets an actionable error.
- [x] Stream throttled model/tool activity and structured tool progress; retain completed steps beside the final answer.
- [x] Persist outbound payloads before sending. Track pending/sending/sent/failed state, attempts, retry time, and Telegram message IDs.
- [x] Resume pending delivery after restart with rate-limit-aware retries. Delivery is at-least-once; a crash after Telegram accepts a send but before SQLite records it can create a duplicate.
- [x] Map completed answer message IDs to durable branch checkpoints. Replies to earlier answers fork from the matching checkpoint under the current policy.

## Accepted limitations for now

- A crash after recording an inbound update but before starting its turn can lose the request. Durable inbox scheduling/recovery is deferred; users may resend.
- A crash after persisting an agent result but before creating its outbox entry can lose delivery. Atomic result-to-outbox handoff/reconciliation is deferred.
- Restart recovery covers already-persisted outbox entries only; it does not recover either gap or rerun side-effecting tools.

## Acceptance checks

- [x] Duplicate updates do not start duplicate turns; passive group context does not start any turn.
- [x] Unlisted users and other groups cannot start turns or add passive context; empty allowlist denies all agent requests.
- [x] Unauthorized chats can request access but cannot run tools; cancellation and reset affect only their target chat.
- [x] HTML and long-output paths work; missing Morsel configuration produces a clear error.
- [x] Restart before/after send recovers already-persisted outbox entries without replaying coding tools. The accepted pre-turn/pre-outbox loss windows are not recovery acceptance requirements.
- [x] Reply forks restore the correct branch and progress; rate limits respect Telegram retry delays.
