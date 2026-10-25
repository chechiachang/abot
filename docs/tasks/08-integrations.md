# 08. Domain integrations and generated media

Depends on: 02–07. Implement each integration independently; disabled integrations must not block startup.

## 1. Quotes and expenses

- [ ] Add `/t` symbol/source parsing and normalized quote output with timestamp, currency, and source; never invent unavailable values.
- [ ] Implement Yahoo Finance stocks/crypto, Taiwan stocks/ETFs, MAX crypto, Frankfurter FX, and Bank of Taiwan cash/spot rates.
- [ ] Set source-specific timeouts, cache freshness, rate-limit handling, and explicit partial/error results. Endpoint choices and symbol conventions: TODO.
- [ ] Bundle the Otter expense-management skill/CLI; configure tokens through secret references and grant only required tools. Package/API and supported expense operations: TODO.

## 2. Polymarket

- [ ] Verify the separate Pi package/API: TODO. Start with public queries, then subscriptions with reconnect/cancel/cleanup.
- [ ] Add account/trading operations only after defining host-side account/chat authorization and explicit operation approval rules: TODO.
- [ ] Enforce authority at dispatch, not in prompts/skills; keep signing credentials outside model context and logs.
- [ ] Define duplicate-operation/recovery handling; never automatically replay an uncertain trade after restart.

## 3. Experimental image generation

- [ ] Verify internal Codex endpoint compatibility and account entitlement separately before enabling the feature. Endpoint/auth contract: TODO; unsupported setups fail explicitly.
- [ ] Support text-to-image only: no edits, reference images, batches, or transparency.
- [ ] Store generated images as private durable artifacts and deliver through Telegram's outbox; enforce ownership, retention, size limits, and cancellation.
- [ ] Recover pending image delivery without regenerating the image unnecessarily.

## Acceptance checks

- [ ] Quote fixtures validate symbols, units, stale data, missing sources, and rates.
- [ ] Expense/trading operations cannot bypass host policy; credentials remain redacted.
- [ ] Subscriptions clean up on cancellation; uncertain side effects are not replayed.
- [ ] Image generation is explicitly experimental; restart recovers authorized artifact delivery.
