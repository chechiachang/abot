# abot plan

## Goal

Build a Telegram bot backed by a Pi LLM agent that can also perform coding-agent tasks: inspect repositories, edit files, run commands, and run checks.

## Stack

1. Go, with [google/adk-go](https://github.com/google/adk-go) for the core agentic flow.
2. [Viper](https://github.com/spf13/viper) for configuration, [Cobra](https://github.com/spf13/cobra) for the CLI, and [Logrus](https://github.com/sirupsen/logrus) for logging.
3. Telegram Bot API as the first bridge; keep bridge-specific routing and delivery separate from agent execution.
4. Pi as the LLM/coding-agent integration behind an adapter.
5. Azure OpenAI through Microsoft Foundry as the first supported LLM provider, using an OpenAI-compatible base URL such as `https://myopenai.openai.azure.com/openai/v1`.

## Integration to resolve

Define and validate the Go-to-Pi boundary before implementation: subprocess/RPC or a separate Pi service. ADK owns the application flow; the adapter exposes Pi requests, coding tasks, progress, results, and cancellation without duplicating Pi's internal execution loop. Decide which runtime owns conversation state and recovery.

## Scope

Implement the capabilities listed below without a web UI or browser-specific administration workflows. Administration uses Cobra commands and Viper configuration. Checkboxes track implementation, not feature selection.

## Implementation order

Detailed tasks: [implementation index](tasks/README.md). Follow the numbered files in order.

### 1. Go foundation and Pi adapter

- [ ] Set up Go ADK, Cobra commands, Viper configuration, and Logrus secret/URL-redacted stderr logging.
- [ ] Select the Pi package/transport candidate and configure Azure OpenAI through Foundry with an OpenAI-compatible base URL, credentials, and model/deployment name before live validation.
- [ ] Validate the Go–Pi boundary using Azure with one prompt and one repository coding task; verify provider/tool compatibility and define execution events, cancellation, state ownership, and error contracts.
- [ ] Load file-based system instructions and persona (`SYSTEM.md` / `SOUL.md`).
- [ ] Establish CI checks for Go and any retained Pi/Node components.

### 2. Headless administration and authorization

- [ ] Start with deny-by-default `telegram.allowed_user_ids` and one `telegram.default_group_id`; private requests require an allowed sender, group requests require both. Administrator identity does not bypass these checks. Add private-user/group/channel grants later.
- [ ] Implement global capability limits, shared defaults, and per-chat overrides for tools, models, skills, and MCP access.
- [ ] Configure provider API keys/OAuth, model availability, explicit default/chat models, and thinking levels; never silently fall back on missing selections.
- [ ] Support Azure OpenAI through Foundry first; expose its base URL, credentials, and model/deployment selection through Viper configuration and Cobra commands. Add other providers afterward.
- [ ] Provide reusable Pi OAuth login integration/extension with local CLI interaction; keep credentials out of model input and logs.
- [ ] Add CLI chat listing/search, readiness diagnostics, policy validation/review/apply, audit records, and historical import reports.
- [ ] Replace web administration and private `/admin` links with administrator CLI commands. No web UI, passkeys, browser sessions, or browser CSRF workflow.

### 3. Durable agent and coding runtime

- [ ] Integrate Pi Durable per-chat SQLite conversations, retries, compaction, steering, follow-ups, cancellation, and restart recovery.
- [ ] Enable permission-filtered `read`, `bash`, `edit`, and `write` coding tools. Document process filesystem permissions; tools are not inherently workspace-isolated.
- [ ] Add Agent Skills discovery and global/default/per-chat selection; skills do not grant tool access.
- [ ] Add branch-aware structured progress with pending, in-progress, completed, and blocked steps, restored after branch changes and compaction.
- [ ] Expose a read-only CLI durable inspector for branches, entries, generation/tool tasks, records, and raw details.

### 4. Telegram bridge and reliable delivery

- [ ] Enforce the user-ID allowlist in private chats and the single configured group. Route group mentions/replies; store only allowed users' unaddressed messages in that group as passive context without starting an agent turn.
- [ ] Add `/start`, `/help`, `/id`, `/cancel`, and `/reset`.
- [ ] Add `/allow` access requests approved through the administrator CLI instead of web approval links.
- [ ] Implement polling retries, rate-limit handling, fatal token/competing-poller detection, and startup/shutdown administrator notifications.
- [ ] Implement Telegram HTML rendering and Morsel publication for output above 1,000 characters, without splitting; require a Morsel key for long output.
- [ ] Render live model/tool activity and multi-step progress; retain completed progress beside the final answer.
- [ ] Add durable response outbox and restart recovery for persisted outbox entries. Track at-least-once delivery; do not automatically replay interrupted side-effecting tools. Accept unrecoverable pre-turn and result-to-outbox crash gaps for now; inbox recovery and atomic result handoff are deferred.
- [ ] Map replies to earlier completed answers to durable branch checkpoints and fork the conversation.

### 5. Tool orchestration and MCP

- [ ] Add administrator-managed stdio/HTTP MCP connections, capability discovery, and per-chat filtering through CLI/config; include Chrome DevTools and Firecrawl configurations.
- [ ] Integrate JavaScript Codemode orchestration in Pi's native sandbox with a host-owned deadline; do not treat it as a sandbox for all coding tools.

### 6. Media input

- [ ] Accept bounded images and index new images from approved channels for on-demand reading; channel posts must not start agent turns.
- [ ] Convert document input locally to Markdown with AnyDoc; reject scanned documents requiring hosted OCR.
- [ ] Transcribe voice/audio locally with Whisper and FFmpeg.

### 7. URL reading and article publishing

- [ ] Add agent-driven `read_url_content` and a reusable library/CLI with public HTTP(S)-only validation, bounded output, deadlines, and cancellation.
- [ ] Add source-aware extraction for YouTube captions/audio, Twitter/X, Threads, Reddit, Truth Social, PTT, news, GitHub, public Google Docs, PDFs, and Office/document URLs.
- [ ] Add Playwright and browser-impersonating HTTP fallbacks; support optional Firecrawl fallback with a configured key and explicit external-transfer configuration.
- [ ] Return structured diagnostics for source failures, unavailable captions, media limits, timeout, and cancellation.
- [ ] Add `/f` Taiwan Traditional Chinese article rewriting from text, reply/media context, or up to four URLs; publish to Morsel only when all required sources succeed.

### 8. Domain integrations and generated media

- [ ] Add `/t` quotes for Yahoo Finance stocks/crypto, Taiwan stocks/ETFs, MAX crypto, Frankfurter FX, and Bank of Taiwan cash/spot rates.
- [ ] Bundle the Otter expense-management skill, CLI, and token configuration.
- [ ] Integrate the separate Polymarket Pi package for public queries, subscriptions, and host-authorized account/trading operations; enforce account/chat authorization in host code.
- [ ] Add experimental text-to-image generation with private artifacts and durable Telegram image delivery. Verify internal Codex endpoint compatibility and account entitlement separately; no editing, references, batches, or transparency.

### 9. Production observability and releases

- [ ] Extend redacted logging with optional Logfire tracing for requests, URL extraction, tools, token usage, retries, compaction, and delivery.
- [ ] Package Docker Compose deployment with persistent session state and coding workdir, non-root runtime, health checks, and cached builds; no web UI service.
- [ ] Add container publication/deployment and versioned Go releases; retain Changesets-based npm releases only for separately published Pi/Node packages.
- [ ] Verify end-to-end Telegram coding, permissions, cancellation, forks, restart recovery, media delivery, and selected integrations before release.
