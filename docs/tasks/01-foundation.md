# 01. Foundation and Pi adapter

Depends on: none. Gate: validate the Go–Pi boundary before building later features.

## 1. Bootstrap

- [ ] Create the Go module and Cobra entry point; wire Viper and Logrus.
- [ ] Add configuration precedence: CLI flags > environment > config file > defaults. Validate required values on startup.
- [ ] Send logs to stderr; redact tokens, credentials, URL userinfo, and sensitive query parameters. Keep protocol output separate.
- [ ] Establish local setup, format, lint, test, and build commands; replace matching `AGENTS.md` TODOs once verified.

## 2. Pi selection and first provider

- [ ] Identify the exact Pi package, version, runtime requirements, and supported transport: TODO. Compare subprocess/RPC with a separate service; record the selected boundary and reason.
- [ ] Configure Azure OpenAI through Foundry before the live prototype: base URL, secret reference, model/deployment, and request timeout. Example base URL: `https://myopenai.openai.azure.com/openai/v1`.
- [ ] Fail explicitly on missing model, invalid configuration, or authentication failure; do not silently substitute a provider/model.
- [ ] Load `SYSTEM.md` and optional `SOUL.md` from configured paths. Define missing-file behavior and instruction ordering; keep secrets out of model instructions.

## 3. Validate integration with Azure

- [ ] Prototype one prompt and one disposable-repository coding task before committing to a transport.
- [ ] Verify Pi supports the configured OpenAI-compatible endpoint, authentication, streaming, and coding-tool calls. Record exact configuration/API mapping: TODO after validation.
- [ ] Keep ADK responsible for application routing, policy checks, and lifecycle. Pi owns its internal model/tool execution loop.
- [ ] Define adapter operations for start, input, progress, result, cancel, and close. Include request/chat IDs and typed retryable, terminal, and cancelled errors.
- [ ] Decide conversation/checkpoint ownership and restart reconciliation: TODO after prototype. Avoid parallel sources of truth.
- [ ] Propagate cancellation and deadlines; clean up child processes/connections and distinguish adapter failure from model failure.

## 4. Checks and CI

- [ ] Add CI for established Go checks and any retained Pi/Node component; pin dependencies/runtime versions.
- [ ] Check config precedence, log redaction, cancellation, and adapter error handling without live credentials.
- [ ] Run opt-in Azure smoke checks: prompt, streamed completion, file edit, and repository check in a disposable workdir.

Done when the CLI completes both smoke tasks, cancellation terminates execution, and the chosen transport/state contract is documented. Live smoke command: TODO after bootstrap.
