# 06. Media input

Depends on: 03–04.

## 1. Shared ingestion

- [ ] Define configurable byte/type/duration/page/output limits, temporary-file cleanup, deadlines, and cancellation. Concrete limits: TODO after dependency validation.
- [ ] Persist attachment metadata and ownership; recheck chat authorization before download/read. Keep original binary content out of logs.
- [ ] Convert supported media into bounded agent inputs; return structured unsupported/oversize/conversion errors.

## 2. Images, documents, and audio

- [ ] Accept bounded images for supported models and index images from approved channels for on-demand reading. Channel posts must not start agent turns.
- [ ] Convert documents locally with AnyDoc to Markdown. Verify package/CLI and supported formats: TODO; reject scanned documents requiring hosted OCR.
- [ ] Transcribe voice/audio locally with Whisper and FFmpeg. Choose runtime/model and setup instructions: TODO; enforce duration/output limits and process cancellation.
- [ ] Retain extracted text/media references needed for replies and `/f`; define retention and private artifact cleanup.

## Acceptance checks

- [ ] Approved-channel indexing starts no generation; unauthorized reads are rejected.
- [ ] Oversized/unsupported media is rejected before expensive processing where possible.
- [ ] Document/audio conversion runs locally; scanned-only documents fail without hosted OCR fallback.
- [ ] Cancellation removes temporary files/processes; conversion errors remain usable in Telegram.
