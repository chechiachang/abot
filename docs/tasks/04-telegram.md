# 04. Telegram bridge and reliable delivery

Depends on: 01–03. Gate: bridge routing/delivery must remain separate from agent execution.

## 1. Inbound routing and commands

- [ ] Configure the bot token and administrator notification destination without logging secrets. Select Telegram client library: TODO.
- [ ] Enforce `telegram.allowed_user_ids` using Telegram sender IDs, not usernames. Private agent requests require a listed sender; group requests also require `telegram.default_group_id`. Support only that one group initially; reject missing/anonymous sender identities for user-triggered requests.
- [ ] Deduplicate update IDs durably and advance polling offsets only after accepted updates are recorded.
- [ ] Route authorized private messages and default-group mentions/replies to the runtime. Store unaddressed messages from allowed users in that group as passive context without starting a turn; discard other group context.
- [ ] Implement `/start`, `/help`, `/id`, `/cancel`, and `/reset`; define reset behavior using a fresh durable conversation/checkpoint without deleting audit history.
- [ ] Add `/allow` requests for unauthorized chats without exposing agent access; administrators approve/reject through the CLI. Approval cannot bypass the configured user allowlist or single-group limit.
- [ ] Retry polling transient failures with bounded backoff; respect rate limits and stop/report fatal token or competing-poller failures.
- [ ] Send configured startup/shutdown administrator notifications; handle graceful shutdown and cancellation.

## 2. Rendering and delivery

- [ ] Escape Telegram HTML correctly and allow only supported formatting. Publish output above 1,000 characters through Morsel rather than splitting messages.
- [ ] Configure/validate Morsel credentials and define an actionable long-output failure when unavailable. API contract and length-count rule: TODO.
- [ ] Stream throttled model/tool activity and structured progress; retain completed progress beside the final answer.
- [ ] Persist outbound payloads and delivery state before sending. Track pending/sending/sent/failed status, attempts, and Telegram message IDs.
- [ ] Resume pending delivery after restart with rate-limit-aware retries. Document at-least-once behavior and the possible duplicate-send window.
- [ ] Map completed answer message IDs to durable branch checkpoints. Replies to earlier answers fork from the matching checkpoint under the current policy.

## Accepted limitations for now

- A crash after recording an inbound update but before starting its turn can lose the request. Durable inbox scheduling/recovery is deferred; users may resend.
- A crash after persisting an agent result but before creating its outbox entry can lose delivery. Atomic result-to-outbox handoff/reconciliation is deferred.
- Restart recovery covers already-persisted outbox entries only; it does not recover either gap or rerun side-effecting tools.

## Acceptance checks

- [ ] Duplicate updates do not start duplicate turns; passive group context does not start any turn.
- [ ] Unlisted users and other groups cannot start turns or add passive context; empty allowlist denies all agent requests.
- [ ] Unauthorized chats can request access but cannot run tools; cancellation and reset affect only their target chat.
- [ ] HTML and long-output paths work; missing Morsel configuration produces a clear error.
- [ ] Restart before/after send recovers already-persisted outbox entries without replaying coding tools. The accepted pre-turn/pre-outbox loss windows are not recovery acceptance requirements.
- [ ] Reply forks restore the correct branch and progress; rate limits do not flood Telegram.
