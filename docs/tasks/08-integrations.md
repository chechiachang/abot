# 08. Domain integrations and generated media

Depends on: 02–07. Implement each integration independently; disabled integrations must not block startup.

## 1. Quotes and expenses

- [x] Add `/t` symbol/source parsing and normalized quote output with timestamp, currency, source, and stale indicator; never invent unavailable values.
- [x] Implement Yahoo Finance stocks/crypto, Taiwan stocks/ETFs, MAX crypto, Frankfurter FX, and Bank of Taiwan cash/spot rates.
- [x] Set source-specific timeouts/cache freshness, rate-limit handling, and explicit per-source error results.
- [ ] Bundle the Otter expense-management skill/CLI; configure tokens through secret references and grant only required tools. Package/API and supported expense operations: TODO.

Quote adapters: Yahoo Finance chart API (`YAHOO:<ticker>`; plain 4–6 digit Taiwan tickers default to `.TW`, or specify `.TWO`), MAX ticker API (`MAX:BTC/TWD`), Frankfurter latest (`FX:USD/TWD` or `USD/TWD`), and Bank of Taiwan exchange-rate page (`BOT:USD`). Responses are bounded and use source-specific cache windows; source timestamps remain visible. HTTP 429 is reported as `rate_limit` without retrying.

## 2. Polymarket

- [x] Implement a bounded Go public market query client and market WebSocket subscription with reconnect/cancel/cleanup.
- [ ] Add account/trading operations only after defining host-side account/chat authorization and explicit operation approval rules: TODO.
- [ ] Enforce authority at dispatch, not in prompts/skills; keep signing credentials outside model context and logs.
- [ ] Define duplicate-operation/recovery handling; never automatically replay an uncertain trade after restart.

Public query uses Gamma `/markets` with a 100-market cap and local filtering; subscriptions use the public CLOB market WebSocket. Account/trading APIs are not exposed pending host approval semantics; no signing credentials or uncertain trade recovery are implemented.

## 3. Experimental image generation

- [ ] Verify internal Codex endpoint compatibility and account entitlement separately before enabling the feature. Endpoint/auth contract: TODO; unsupported setups fail explicitly.
- [ ] Support text-to-image only: no edits, reference images, batches, or transparency.
- [ ] Store generated images as private durable artifacts and deliver through Telegram's outbox; enforce ownership, retention, size limits, and cancellation.
- [ ] Recover pending image delivery without regenerating the image unnecessarily.

## Acceptance checks

- [x] Quote fixtures validate symbols, currencies/rates, stale data, rate limits, and missing source values.
- [ ] Expense/trading operations cannot bypass host policy; credentials remain redacted.
- [x] Public subscriptions clean up on cancellation.
- [ ] Uncertain trading side effects are not replayed after restart; no trading operations are enabled.
- [ ] Image generation is explicitly experimental; restart recovers authorized artifact delivery.
