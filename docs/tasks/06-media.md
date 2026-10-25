# 06. Media input

Depends on: 03–04.

## 1. Shared ingestion

- [x] Define configurable byte/type/duration/page/output limits, private temporary files, deadlines, cancellation, and 30-day attachment retention.
- [x] Persist private attachment metadata and ownership; authorize chat/sender before download and recheck ownership or approved-channel status before read. Binary content is never logged.
- [x] Convert supported media into bounded ADK inputs; return structured unsupported/oversize/conversion errors.

## 2. Images, documents, and audio

- [x] Accept bounded JPEG/PNG/WebP images for Azure OpenAI chat-completions models and index images from approved channels for on-demand reading. Channel posts never start agent turns.
- [x] Convert PDF locally with Poppler (`pdfinfo`, `pdftotext`) and DOCX/ODT/RTF with Pandoc to Markdown; reject scanned PDFs requiring OCR.
- [x] Transcribe voice/audio locally with FFmpeg and `whisper-cli` when a local model is configured; enforce duration/output limits and process cancellation. Missing binaries/model produce a clear disabled-feature error.
- [x] Retain attachment references for replies and `/f` through the durable session; delete private original artifacts and metadata after 30 days.

## Acceptance checks

- [x] Approved-channel indexing starts no generation; unauthorized channel and cross-user reads are rejected.
- [x] Oversized/unsupported media is rejected before download or conversion where possible.
- [x] Document/audio conversion runs locally; scanned-only PDFs fail without hosted OCR fallback.
- [x] Cancellation removes conversion temporary files/processes; conversion errors remain usable in Telegram.

Optional local dependencies: Pandoc for DOCX/ODT/RTF, Poppler for PDF, and FFmpeg plus `whisper-cli` and a model file for audio transcription. Images are sent as bounded inline data to the configured model provider.
