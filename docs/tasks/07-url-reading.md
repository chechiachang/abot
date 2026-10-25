# 07. URL reading and article publishing

Depends on: 04–06; reuse MCP/Firecrawl configuration from 05 where applicable.

## 1. Reader boundary

- [x] Implement a reusable URL-reading library, CLI, and permission-filtered `read_url_content` tool with the same result/error contract.
- [x] Allow only public HTTP(S) targets. Reject credentials and private/loopback/link-local destinations; revalidate redirects and resolved dial targets to prevent DNS-rebinding bypasses.
- [x] Bound redirects, downloads, extracted output, media duration, and total deadline; propagate cancellation to every subprocess/network fallback.
- [x] Return source URL/title/content plus structured failure categories for unsupported source, unavailable captions, limits, timeout, cancellation, and extraction failure.

## 2. Extraction order

- [x] Start with ordinary HTML/news and public GitHub/Google Docs; normalize content to useful text/Markdown.
- [x] Add PDF and Office/document URLs using local media converters.
- [x] Add source loaders for Twitter/X, Threads, Reddit, Truth Social, and PTT. Record endpoint/parser choices and fixture coverage below.
- [x] Add YouTube captions first and bounded audio transcription fallback; report unavailable captions distinctly.
- [x] Add headless Chromium CDP/browser-TLS fallback; all HTTPS page/subresource requests use the same public-only DNS-revalidating CONNECT proxy, with aggregate byte and connection limits.
- [x] Add optional Firecrawl only with a configured key and explicit external-transfer setting. Document when it is used and what content is sent.

## 3. `/f` publishing

- [x] Gather text, replied-to text/media, or up to four URLs; resolve required sources before generation.
- [x] Rewrite in Taiwan Traditional Chinese with source attribution and configured model/tool permissions.
- [x] Publish through Morsel only after all required sources succeed; preserve structured diagnostics and do not publish a partial article on source failure.

## Acceptance checks

- [x] Direct/redirected private targets, DNS changes, and browser subrequests cannot bypass public-only validation.
- [x] Parser fixtures cover each source family; live checks are opt-in and do not require credentials in CI.
- [x] Timeouts/limits/cancellation stop all fallbacks; Firecrawl remains off without explicit transfer configuration.
- [x] `/f` handles text/replies/media/four URLs and publishes nothing when a required source fails.

Source choices and fixture coverage: GitHub REST README plus `raw.githubusercontent.com` blobs; public Google Docs text export; Reddit public JSON; Truth Social status API; X, Threads, PTT, and news readable HTML; YouTube Innertube captions followed by optional `yt-dlp` audio through the restricted HTTPS proxy and local FFmpeg/Whisper. Browser fallback uses Chromium CDP rather than a Playwright wrapper. Firecrawl is only used when `FIRECRAWL_API_KEY` is set and `url_reader.firecrawl_external_transfer=true`; it receives the original page URL and performs the remote page fetch/extraction.
