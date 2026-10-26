# Telegram polling gap

## Observed

- The previous turn completed at 14:11:32 (+08:00) and its Telegram reply was delivered at 14:11:33.
- Telegram update `177521382` (`tell me a joke`) is dated 14:11:42.
- The bridge queued its 14-character input at 14:15:23, about 3m41s after Telegram timestamped it. The agent started immediately and completed at 14:15:25.
- Updates `177521383` (`/start`, 14:14:51) and `177521384` (`/allow`, 14:15:01) were also handled at 14:15:23.

This points to a delay before the bridge handled the Telegram updates, not slow model generation. The later repeated-update logs exposed a separate polling cursor bug; the original 3m41 interval alone does not establish why polling was delayed.

## Acknowledgment and cursor mismatch

Telegram confirms updates implicitly: a subsequent `getUpdates` request must send lowercase `offset` greater than the update ID. There is no separate acknowledgment call. The polling request previously omitted JSON tags, so it sent `Offset`/`Timeout`; Telegram ignored the offset and repeatedly returned the first pending event. The request now uses the Bot API's lowercase parameter names.

The repeated-update logs also showed a stored offset of `755726481` while Telegram returned IDs around `177521363`. Startup now polls once without an offset, then persists the highest received update ID plus one to rebase the local cursor before normal polling. A `Telegram polling offset rebased` log confirms this. Keep separate SQLite state for separate bot tokens.

## Debug procedure

1. Check that the expected local `telegram serve` process is running with the intended bot configuration and SQLite state. The startup log includes `Telegram bot connected` and `Telegram polling started` with its persisted `update_offset`.
2. Avoid calling `getUpdates` manually while the bot is running. Telegram permits one active long-poll consumer per bot token; a second poller can interfere. Do not put a real bot token in shared logs or chat.
3. Send a fresh private message and inspect stderr for `received Telegram bot event`. It includes `update_id` and `message_age_seconds` (approximate; Telegram message dates have one-second precision). No message body is logged.
4. Follow the same event through `queued Telegram turn`, `processing queued Telegram turn`, `agent turn started/completed`, and `Telegram message delivered`:
   - No receive log: verify the poller is active, the bot/token is the intended one, and inspect polling retry/fatal-error logs.
   - Receive log with high age: Telegram delivered a backlog to the process; inspect poll retries, process restarts, and whether another poller is competing.
   - Receive log but no queued turn: inspect authorization, command/group routing, and update-recording warnings.
   - Queued turn but late agent start/completion: investigate runtime/model latency.
   - Agent completed promptly but delivery is late: inspect outbox delivery/retry logs.
5. Compare `update_id` with startup `update_offset`. The bridge resumes from the SQLite offset; using a different SQLite file changes the cursor being inspected. A `duplicate Telegram bot event ignored` log means that update ID was already recorded locally.

Logfire's environment labels traces; it does not ship the Logrus stderr messages. Use local serve logs for the receive/queue/delivery timeline.
