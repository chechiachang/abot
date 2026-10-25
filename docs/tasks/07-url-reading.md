# 07. URL reading and article publishing

Depends on: 04–06; reuse MCP/Firecrawl configuration from 05 where applicable.

## 1. Reader boundary

- [ ] Implement a reusable URL-reading library, CLI, and permission-filtered `read_url_content` tool with the same result/error contract.
- [ ] Allow only public HTTP(S) targets. Reject credentials and private/loopback/link-local destinations; revalidate redirects and resolved dial targets to prevent DNS-rebinding bypasses.
- [ ] Bound redirects, downloads, extracted output, media duration, and total deadline; propagate cancellation to every subprocess/network fallback.
- [ ] Return source URL/title/content plus structured failure categories for unsupported source, unavailable captions, limits, timeout, cancellation, and extraction failure.

## 2. Extraction order

- [ ] Start with ordinary HTML/news and public GitHub/Google Docs; normalize content to useful text/Markdown.
- [ ] Add PDF and Office/document URLs using local media converters.
- [ ] Add source loaders for Twitter/X, Threads, Reddit, Truth Social, and PTT. Record endpoint/parser choices and fixture coverage: TODO.
- [ ] Add YouTube captions first and bounded audio transcription fallback; report unavailable captions distinctly.
- [ ] Add Playwright and browser-impersonating HTTP fallbacks; apply the same public-target/limit rules to browser subrequests.
- [ ] Add optional Firecrawl only with a configured key and explicit external-transfer setting. Document when it is used and what content is sent.

## 3. `/f` publishing

- [ ] Gather text, replied-to text/media, or up to four URLs; resolve required sources before generation.
- [ ] Rewrite in Taiwan Traditional Chinese with source attribution and configured model/tool permissions.
- [ ] Publish through Morsel only after all required sources succeed; preserve structured diagnostics and do not publish a partial article on source failure.

## Acceptance checks

- [ ] Direct/redirected private targets, DNS changes, and browser subrequests cannot bypass public-only validation.
- [ ] Parser fixtures cover each source family; live checks are opt-in and do not require credentials in CI.
- [ ] Timeouts/limits/cancellation stop all fallbacks; Firecrawl remains off without explicit transfer configuration.
- [ ] `/f` handles text/replies/media/four URLs and publishes nothing when a required source fails.
