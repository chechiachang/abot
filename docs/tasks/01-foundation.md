# 01. Go/ADK foundation and coding agent

Depends on: none. Gate: the Go CLI can call the configured model and route a repository task through an ADK coding sub-agent.

## 1. Bootstrap

- [x] Create the Go module and Cobra entry point; wire ADK-Go, Viper, and Logrus.
- [x] Implement configuration precedence: CLI flags > environment > config file > defaults. Validate required values on startup.
- [x] Send logs to stderr; redact tokens, credentials, URL userinfo, and sensitive query parameters.
- [x] Load `SYSTEM.md` and optional `SOUL.md` from configured paths. Missing `SYSTEM.md` is an error; missing `SOUL.md` is allowed. Keep secrets out of instructions.
- [x] Establish local setup, format, lint, test, and build commands; replace matching `AGENTS.md` TODOs once verified.

## 2. Azure OpenAI model

- [x] Pin ADK-Go and implement its `model.LLM` interface for Foundry's OpenAI-compatible Chat Completions API; keep the adapter in Go.
- [x] Configure the `/openai/v1` base URL, API-key secret reference, model/deployment name, and request timeout through Viper/Cobra.
- [x] Map ADK content, function declarations/calls, streaming deltas, usage, and provider errors to the OpenAI-compatible API.
- [x] Fail explicitly on missing model/key, invalid configuration, or authentication failure. Never silently substitute a provider or model.

## 3. ADK workflow and execution

- [x] Build a coordinator `llmagent` with a coding `llmagent` sub-agent; use ADK delegation rather than an external process or second runtime.
- [x] Keep application routing, policy checks, tool dispatch, lifecycle, and cancellation in the Go host/ADK workflow.
- [x] Define stable request/chat IDs, progress/result events, and typed provider/authentication/configuration/cancelled errors.
- [x] Use the ADK session service as the conversation source of truth; keep host-owned task/progress records separate and avoid parallel conversation histories.
- [x] Propagate context cancellation and deadlines through model requests and coding tools; distinguish provider failures from tool failures.

## 4. Checks and CI

- [x] Add Go-only CI for formatting, tests, and build; pin Go and module versions.
- [x] Check config precedence, log redaction, Azure request mapping, streaming, cancellation, and typed error handling without live credentials.
- [x] Add opt-in Foundry smoke checks for a prompt, streamed response, coding-tool file edit, and repository check in a disposable workdir (`sh scripts/foundry-smoke.sh`).

Done: the CLI completes a streamed prompt and a repository coding task through the ADK sub-agent; cancellation, state ownership, and provider errors are covered by local tests. Live smoke: `sh scripts/foundry-smoke.sh` after exporting Azure settings; set `AZURE_OPENAI_REASONING_EFFORT=none` when required by the deployment.
