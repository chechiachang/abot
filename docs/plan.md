# abot plan

## Goal

Build a Telegram bot backed entirely by Go and Google ADK-Go. An ADK coordinator can delegate repository work to a Go coding sub-agent that inspects repositories, edits files, runs commands, and checks results.

## Stack

1. Go, with [google/adk-go](https://github.com/google/adk-go) for the core agentic flow.
2. [Viper](https://github.com/spf13/viper) for configuration, [Cobra](https://github.com/spf13/cobra) for the CLI, and [Logrus](https://github.com/sirupsen/logrus) for logging.
3. Telegram Bot API as the first bridge; keep bridge-specific routing and delivery separate from agent execution.
4. Go/ADK coordinator and coding sub-agent; register coding and integration tools directly in ADK.
5. Azure OpenAI through Microsoft Foundry as the first supported LLM provider. Implement a Go `model.LLM` adapter for its OpenAI-compatible Chat Completions API, using a base URL such as `https://myopenai.openai.azure.com/openai/v1`.

## Integration to resolve

Keep model access, agent orchestration, coding tools, and lifecycle in Go. ADK owns routing, sub-agent delegation, policy checks, tool dispatch, and cancellation; the Azure adapter only translates ADK model requests/responses to the OpenAI-compatible API. Use the ADK session service as the conversation source of truth and persist host-owned task/progress records separately.

## Scope

Implement the capabilities listed below without a web UI or browser-specific administration workflows. Administration uses Cobra commands and Viper configuration. Checkboxes track implementation, not feature selection.

## Implementation order

Detailed tasks: [implementation index](tasks/README.md). Follow the numbered files in order.

### 1. Go/ADK foundation and coding agent

- [x] Set up Go ADK, Cobra commands, Viper configuration, and Logrus secret/URL-redacted stderr logging.
- [x] Implement the Azure OpenAI `model.LLM` adapter and configure the Foundry base URL, secret reference, model/deployment, and timeout before live validation.
- [x] Validate a streamed prompt and repository coding task through the ADK coordinator and coding sub-agent; verify tool calls, cancellation, state ownership, and error handling.
- [x] Load file-based system instructions and persona (`SYSTEM.md` / `SOUL.md`).
- [x] Establish Go-only CI checks with pinned toolchain/dependencies.

### 2. Headless administration and authorization

- [x] Start with deny-by-default `telegram.allowed_user_ids` and one `telegram.default_group_id`; private requests require an allowed sender, group requests require both. Administrator identity does not bypass these checks. Persisted grants cannot expand those limits.
- [x] Implement global capability limits, shared defaults, and per-chat overrides for tools, models, skills, and MCP access.
- [x] Configure explicit default/chat models and validate configured thinking levels; never silently fall back on missing selections.
- [x] Support Azure OpenAI through Foundry; expose its base URL, credentials, model/deployment, and reasoning effort through Viper/Cobra. Keep credentials out of output and audit records.
- [x] Define provider authentication availability: Azure API keys use environment configuration; OAuth login is unsupported for this provider.
- [x] Add headless policy validate/review/apply, capability/grant/provider/model diagnostics, configured-chat search, and audit records. Durable chat search/import reports move to task 03; Telegram access request administration moves to task 04.
- [x] Replace web administration and private `/admin` links with local administrator CLI commands. No web UI, passkeys, browser sessions, or browser CSRF workflow.

### 3. Durable agent and coding runtime

- [x] Persist ADK per-chat sessions in SQLite; add retries, compaction, steering, follow-ups, cancellation, and restart recovery.
- [x] Enable permission-filtered `read`, `bash`, `edit`, and `write` coding tools. Document process filesystem permissions; tools are not inherently workspace-isolated.
- [x] Add Agent Skills discovery and global/default/per-chat selection; skills do not grant tool access.
- [x] Add branch-aware structured progress with pending, in-progress, completed, and blocked steps, restored after branch changes and compaction.
- [x] Expose a read-only CLI durable inspector for branches, entries, generation/tool tasks, records, and redacted raw details.

### 4. Telegram bridge and reliable delivery

- [x] Enforce the user-ID allowlist in private chats and the single configured group. Route group mentions/replies; store only allowed users' unaddressed messages in that group as passive context without starting an agent turn.
- [x] Add `/start`, `/help`, `/id`, `/cancel`, and `/reset`.
- [x] Add `/allow` access requests reviewed through the administrator CLI; policy allowlist limits still apply.
- [x] Implement polling retries, rate-limit handling, fatal token/competing-poller detection, and startup/shutdown administrator notifications.
- [x] Implement Telegram HTML rendering and Morsel publication for output above 1,000 characters, without splitting; require Morsel credentials for long output.
- [x] Render throttled model/tool activity and structured progress; retain completed tool progress beside the final answer.
- [x] Add durable response outbox and restart recovery for persisted outbox entries. Track at-least-once delivery; do not automatically replay interrupted side-effecting tools. Accept unrecoverable pre-turn and result-to-outbox crash gaps for now; inbox recovery and atomic result handoff are deferred.
- [x] Map replies to earlier completed answers to durable branch checkpoints and fork the conversation.

### 5. Tool orchestration and MCP

- [x] Add administrator-managed stdio/HTTP MCP connections, capability discovery, and per-chat filtering through CLI/config; include Chrome DevTools and Firecrawl configurations.
- [x] Implement Go-native tool orchestration with a host-owned deadline, permission checks on every dispatch, bounded output, and cancellation.

### 6. Media input

- [x] Accept bounded images and index new images from approved channels for on-demand reading; channel posts must not start agent turns.
- [x] Convert PDF with Poppler and DOCX/ODT/RTF with Pandoc locally to Markdown; reject scanned documents requiring hosted OCR.
- [x] Transcribe voice/audio locally with FFmpeg and configured `whisper-cli` model.

### 7. URL reading and article publishing

- [x] Add agent-driven `read_url_content` and a reusable library/CLI with public HTTP(S)-only validation, bounded output, deadlines, and cancellation.
- [x] Add source-aware extraction for YouTube captions/audio, Twitter/X, Threads, Reddit, Truth Social, PTT, news, GitHub, public Google Docs, PDFs, and Office/document URLs.
- [x] Add a headless Chromium CDP fallback with public-only, DNS-revalidating HTTPS subrequest proxy; support optional Firecrawl fallback with a configured key and explicit external-transfer configuration.
- [x] Return structured diagnostics for source failures, unavailable captions, media limits, timeout, and cancellation.
- [x] Add `/f` Taiwan Traditional Chinese article rewriting from text, reply/media context, or up to four URLs; publish to Morsel only when all required sources succeed.

### 8. Domain integrations and generated media

- [x] Add `/t` quotes for Yahoo Finance stocks/crypto, Taiwan stocks/ETFs, MAX crypto, Frankfurter FX, and Bank of Taiwan cash/spot rates.
- [ ] Bundle the Otter expense-management skill, CLI, and token configuration.
- [x] Integrate Polymarket Go APIs for public queries and subscriptions with reconnect/cancel/cleanup.
- [ ] Add account/trading operations only after host-side account/chat authorization and explicit operation approval rules are defined; no signing credentials or trade replay are implemented.
- [ ] Add experimental text-to-image generation with private artifacts and durable Telegram image delivery. Verify internal Codex endpoint compatibility and account entitlement separately; no editing, references, batches, or transparency.

### 9. Production observability and releases

- [x] Extend redacted logging with optional Logfire OTLP tracing for requests, URL extraction, tools, token usage, retries, compaction, and delivery.
- [x] Package Docker Compose deployment with persistent session state and coding workdir, non-root runtime, readiness health check, and cached builds; no web UI service.
- [x] Add container publication and versioned Go releases; deployment target remains unconfigured.
- [ ] Verify end-to-end Telegram coding, permissions, cancellation, forks, restart recovery, media delivery, and selected integrations before release.
