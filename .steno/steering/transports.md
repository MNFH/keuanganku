---
inclusion: fileMatch
fileMatchPattern: "internal/messaging/**,internal/wasend/**,internal/telegram/**,internal/whatsappcloud/**"
---
# Transports

These packages deliver `handler.Reply` values to WhatsApp (whatsmeow), Telegram and the WhatsApp Cloud API, and feed inbound messages to `dispatch` in `cmd/api/main.go`. The mistake agents make most often is adding platform logic to `handler`, or sending through one transport directly instead of through `messaging.Router`.

## Purpose
- `messaging.Sender` has two methods: `SendText(ctx, chatID, text)` and `SendDocument(ctx, chatID, data, filename, caption)`. `Router.For(chatID)` picks the transport from the chat ID.
- Chat IDs: Telegram is `tg:<id>` (`TelegramChatID`/`ParseTelegramChatID`), Cloud API is `wac:<wa_id>` (`CloudChatID`/`ParseCloudChatID`), and whatsmeow uses the bare JID string, unprefixed.
- A new transport gets a new prefix. The whatsmeow IDs stay bare so existing `users` rows need no migration.

## Rules
- Cloud API: Meta error `131047` (`reengagementErrorCode`) is surfaced as `messaging.ErrOutsideWindow`. `Client.Reengage` sends the `WHATSAPP_REENGAGE_TEMPLATE` template. A template cannot carry a PDF.
- Keep `ErrOutsideWindow` and `Reengager` in `messaging`, so the scheduler never imports a platform package.
- `whatsappcloud.Webhook` checks `X-Hub-Signature-256` (`validSignature`) on every POST and refuses everything when `WHATSAPP_APP_SECRET` is empty. It answers 200 before processing, because Meta retries slow responses and a retry would record a transaction twice.
- Request bodies are read through `io.LimitReader` (4 MiB). Telegram photos are capped at `maxPhotoBytes` (12 MiB).
- Telegram text goes through `telegram.ToHTML`, which escapes `& < >` before converting `*bold*` and `_italic_`. Otherwise Telegram rejects the whole message.
- Telegram's `chatQueues` runs different chats concurrently but keeps each chat's messages in order. Long polling holds `pollTimeout` = 50 s, and the HTTP client timeout must exceed it.

## Patterns to reuse
- Use plain `net/http` with a small `call`/`postMessage` helper and a typed API error (`telegram.APIError`, `whatsappcloud.graphError`).
- Tests use `httptest` and handcrafted payloads: `webhook_test.go`, `markup_test.go`, `queue_test.go`.

## Do not
- Do not import a transport from `handler` or `scheduler`.
- Do not add an SDK for Telegram or Graph.
- Do not weaken the webhook into accepting unsigned requests "for development".
