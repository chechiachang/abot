# 02. Headless administration and authorization

Depends on: 01. Gate: all agent entry points must use the same authorization policy.

## 1. Policy and configuration

- [ ] Start with Viper configuration `telegram.allowed_user_ids` and one `telegram.default_group_id`. Allow private agent requests only from listed user IDs; group requests require both a listed sender and the configured group ID. Empty allowlist denies all agent requests; administrator identity is not an exception.
- [ ] Add persisted private-user/group/channel grants later. Group grants must not bypass the user allowlist; keep approved-channel ingestion separate from user-triggered requests.
- [ ] Resolve shared defaults and per-chat overrides, then intersect requested tools/models/skills/MCP access with global capability limits. Overrides cannot expand global limits.
- [ ] Require explicit default/chat model selection; validate thinking levels against provider/model support. Missing or unavailable selections fail without fallback.
- [ ] Configure Azure base URL, credential reference, and model/deployment through Viper and Cobra. Keep secret values out of command output, audit records, and model input.
- [ ] Add other provider API-key/OAuth configuration only after Azure works. Integrate reusable Pi OAuth login with local CLI interaction; supported extension API and credential store: TODO.

## 2. Administrator CLI

- [ ] Add command groups for grants, capabilities, providers/models, chats, and diagnostics. Exact names: TODO during CLI design.
- [ ] Add policy validate/review/apply: show effective changes before explicit apply, persist atomically, and audit actor/time/target/result without credentials.
- [ ] Add chat listing/search and effective-policy inspection. Diagnostics explain missing grants, models, credentials, tools, or runtime dependencies.
- [ ] Define how local administrator authority is established: TODO. Never expose administrator commands as unrestricted Telegram/model tools.
- [ ] Add historical import with dry-run validation, version/source metadata, duplicate handling, and imported/skipped/failed counts. Source format: TODO.
- [ ] Handle access requests through CLI approval/rejection; no `/admin` links, web UI, passkeys, browser sessions, or CSRF workflow.

## Acceptance checks

- [ ] Requests outside the configured user allowlist/group limit are rejected, including administrators; later grants preserve these restrictions.
- [ ] An unlisted sender in the default group and a listed sender in another group are rejected. Missing/anonymous user identities cannot pass the allowlist.
- [ ] Per-chat overrides cannot bypass global restrictions; skills cannot grant tools.
- [ ] Invalid policy updates leave the previous policy intact; audit output is redacted.
- [ ] Missing models and unsupported thinking levels produce actionable errors.
- [ ] CLI login/inspection never exposes credentials to agent input or logs.
