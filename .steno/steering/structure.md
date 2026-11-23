# Project structure

## Top-level layout
```
cmd/
  api/            # main: config, stores, transports, the shared dispatch(), scheduler
  smoke/          # manual live Gemini check (text, -image)
  tgsmoke/        # manual Telegram send/receive check
  wacsmoke/       # manual WhatsApp Cloud API check
config/           # config.go: .env → Config, defaults
internal/         # all application packages (below)
.env.example      # documented environment variables
CLAUDE.md         # architecture notes; AGENTS.md mirrors them for other agents
.steno/           # steno specs and steering
```
`internal/repository/` and `internal/service/` are empty leftover folders. Do not put code there.

## `internal/` — application packages
| Package | Responsibility |
|---|---|
| `handler/` | `MessageHandler.Handle(ctx, chatID, Message) Reply`: command parsing, natural-language dispatch, per-sheet lock, Sheets client cache |
| `ai/` | Gemini `Parser` → `Intent`. `Intent.Command()` renders a canonical slash command. `Categories` is the closed vocabulary |
| `model/` | `Transaction`, `Wallet`, `FormatAmount`, `BalanceDelta` |
| `sheets/` | every Google Sheets read and write: wallets, append-only transactions, `/batal`, balance formulas |
| `recap/` | `Compute` totals, `*Range` period helpers, `ParseIndoMonth`, shared by text recaps and the PDF |
| `report/` | `GenerateMonthly(title, txs, wallets)` → PDF bytes with a pie chart |
| `scheduler/` | polling monthly and weekly reports. `SQLDeliveries` records what was sent |
| `userstore/` | MySQL `users` table: chat ID → spreadsheet ID, `AnyUses`, `MigrateFromJSON` |
| `messaging/` | `Sender`, `Router`, chat-ID prefixes, `ErrOutsideWindow`, `Reengager` |
| `wasend/` | whatsmeow `Sender` |
| `telegram/` | Bot API long-poll, `ToHTML`, per-chat `chatQueues` |
| `whatsappcloud/` | Graph API client and the signed `Webhook` |

Dependency direction: `cmd/api` → `handler`/`scheduler` → `sheets`/`recap`/`report`/`ai`/`userstore` → `model`. Transports depend only on `messaging`. `handler` imports no transport.

## Where new code goes
- New slash command → a `cmdXxx` method plus a `case` in `handleCommand` in `internal/handler/message.go`, and a line in `helpMessage()`. Tests go in `internal/handler/message_test.go`.
- New AI action → an `Action` constant and a `Command()` branch in `internal/ai`, tested in `command_test.go`. It must map to an existing slash command.
- New sheet read or write → a method on `sheets.Client` wrapped in `withRecovery`. Unit-testable parts go in `client_test.go`, API round trips in `live_test.go`.
- New period or recap math → `internal/recap`. New PDF content → `internal/report`.
- New transport → its own `internal/<name>` package implementing `messaging.Sender`, registered in `Router`, with its event loop in `cmd/api/main.go` feeding `dispatch`.
- New environment setting → `config/config.go` plus `.env.example`.

## Key conventions
- **The handler never sends:** it returns a `handler.Reply` (Text, or Document + Filename + Caption, or zero for silence). Transports do the delivery.
- **One dispatch path:** every transport funnels into `dispatch` in `cmd/api/main.go`. Platform-specific behaviour goes there, not in `handler`.
- **AI only translates:** `handleNaturalLanguage` parses the message, then runs the resulting command through `handleCommand`. `MessageHandler.parser` may be nil.
- **Amounts:** parse with `parseAmount` and render with `model.FormatAmount` (`.` thousands separator).
- **Wallet names:** resolve with `matchWalletPrefix` (greedy, multi-word) and `matchWalletName` (case-insensitive, canonical name).
- **Locking:** `handleCommand` holds `lockSheet(spreadsheetID)` for the whole command. The Gemini call runs outside the lock.
- **Cache:** the Sheets clients in `MessageHandler.cache` are keyed by spreadsheet ID, not by chat.
- **Delivery to users** outside a reply goes through `messaging.Router`, never through one transport directly.
- **Strings:** use Indonesian, with emoji prefixes and `*bold*`.
