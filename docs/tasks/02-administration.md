# 02. Headless administration and authorization

Depends on: 01. Gate: all agent entry points must use the same authorization policy.

## 1. Policy and configuration

- [x] Start with Viper configuration `telegram.allowed_user_ids` and one `telegram.default_group_id`. Allow private agent requests only from listed user IDs; group requests require both a listed sender and the configured group ID. Empty allowlist denies all agent requests; administrator identity is not an exception.
- [x] Persist private-user/group/channel grants. These grants never bypass the sender allowlist or the single-group limit; approved-channel ingestion is separate from user-triggered requests.
- [x] Resolve shared defaults and per-chat overrides, then intersect requested tools/models/skills/MCP access with global capability limits. Overrides cannot expand global limits.
- [x] Require explicit default/chat model selection; validate thinking levels against provider/model support. Missing or unavailable selections fail without fallback.
- [x] Configure Azure base URL, credential reference, and model/deployment through Viper and Cobra. Keep secret values out of command output, audit records, and model input.
- [x] Azure API keys use environment/secret configuration and safe CLI inspection. Azure API-key OAuth login is unsupported; other providers remain disabled until implemented.

## 2. Administrator CLI

- [x] Add CLI groups for grants, capabilities, providers/models, configured chats, and diagnostics.
- [x] Add policy validate/review/apply: show before/after summaries, persist policy atomically, and audit actor/time/result without credentials.
- [x] Add configured-chat listing/search and effective-policy inspection. Durable conversation search belongs to Task 03.
- [x] Establish local administrator authority as the OS user invoking the CLI. Never expose administrator commands as unrestricted Telegram/model tools.
- [x] Keep administration headless: no `/admin` links, web UI, passkeys, browser sessions, or CSRF workflow. Task 04 owns the CLI lifecycle for Telegram access requests; Task 03 owns historical import after defining durable chat storage.

## Acceptance checks

- [x] Requests outside the configured user allowlist/group limit are rejected, including administrators; grants preserve these restrictions.
- [x] An unlisted sender in the default group and a listed sender in another group are rejected. Missing/anonymous user identities cannot pass the allowlist.
- [x] Per-chat overrides cannot bypass global restrictions; skills cannot grant tools.
- [x] Invalid policy updates leave the previous policy intact; audit output is redacted.
- [x] Missing models and unsupported thinking levels produce actionable errors.
- [x] Provider inspection never exposes credentials; Azure API-key OAuth is explicitly unsupported.
