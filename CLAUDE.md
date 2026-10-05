# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
go build -o keuanganku.exe ./cmd/api   # build
go run ./cmd/api                       # build + run (needs .env and a reachable MySQL)
go vet ./...                           # both currently pass clean
gofmt -l .                             # list unformatted files
```

```bash
go test ./...                                      # all tests
go test ./internal/model                           # one package
go test ./internal/model -run TestFormatAmount     # one test
go test ./internal/model -run 'TestFormatAmount/negative_six_digits' -v   # one subtest
```

Tested: `internal/model` (`FormatAmount`, `BalanceDelta`), `internal/handler` (`parseAmount`),
`internal/ai` (`Intent.Command`, `Mutates`, `normalize`), `internal/telegram` (`ToHTML`), and
`internal/whatsappcloud` (webhook signature verification, the verify handshake, payload
parsing). Still untested but equally dependency-free: `recap.Compute`, the `*Range` helpers,
`ParseIndoMonth`, `matchWalletPrefix`, `parseTransactionRow`, `nextFireTime`.

No test hits a network service, so `ai.Parser.Parse` and every transport's actual I/O are
uncovered. `cmd/smoke` exercises the live Gemini path by hand (text and `-image`); it needs a
real API key and costs quota, so run it deliberately rather than in CI.

`gofmt -l .` lists every file: this is a Windows CRLF checkout and gofmt expects LF. Mostly a
false positive — but a few files do have real alignment drift, so check `gofmt -d <file>`
before assuming. Don't run `gofmt -w .` across the repo; it buries a real diff in noise.

Running the bot requires `MYSQL_DSN` in `.env` (the process calls `log.Fatalf` if it is
missing) plus a Google service-account JSON at `GOOGLE_CREDENTIALS_FILE`
(default `credentials.json`). With the default whatsmeow transport, the first run prints a QR
code to link a WhatsApp account and persists the session in `whatsmeow.db`.

## Architecture

A personal-finance chat bot serving WhatsApp and Telegram, driven by slash commands or by
free-form Indonesian messages and receipt photos.

### Three stores, and which data lives where

This split is the single most important thing to understand, and the README understates it
by calling Sheets an "optional export":

- **Google Sheets is the transaction database**, one spreadsheet per registered chat, with
  two tabs created and headed by `sheets.InitSheets`: `Wallets` (Name, Balance, Created At)
  and `Transactions` (ID, Date, Type, Amount, Wallet, Category, Description, RefID, Direction).
  All reads and writes of financial data go through `internal/sheets`.
- **MySQL holds only the `users` table** — a `chat ID → spreadsheet_id` mapping, auto-migrated
  on startup by `userstore.New`. The database itself must already exist; only the table is created.
- **SQLite (`whatsmeow.db`)** holds the WhatsApp device session, owned by whatsmeow's `sqlstore`.

### Message flow, and the transport boundary

The bot serves **three chat transports**. `handler.Handle(ctx, chatID, handler.Message{...})`
is platform-agnostic: it takes an opaque chat ID, and **never sends anything** — it returns a
`handler.Reply` that is either `Text`, or a `Document` + `Filename` + `Caption`, or zero-value
meaning "send nothing". Keep it that way; it is what let Telegram and the Cloud API be
transports rather than forks.

- `internal/messaging` defines `Sender` (`SendText`, `SendDocument`) and a `Router` that picks
  the transport from the chat ID.
- Three transports: `internal/wasend` (whatsmeow linked device), `internal/telegram`
  (long-polling Bot API), `internal/whatsappcloud` (official Graph API + webhook). All three
  talk plain HTTP where possible rather than wrapper libraries.
- `cmd/api/main.go` holds each event loop, but they all funnel into one `dispatch` function, so
  their behaviour cannot drift apart. Add platform handling there, not in the handler.

**Chat ID scheme:** prefixed per platform (`tg:123456`, `wac:628123456789`) except whatsmeow,
whose IDs stay bare as the JID string they have always been. Prefixing only the newer platforms
meant the existing `users` rows stayed valid and no migration was ever needed — preserve that
asymmetry when adding a transport.

**The 24-hour window (Cloud API only).** Meta rejects free-form messages, documents included,
more than 24 hours after the user's last message. `whatsappcloud` surfaces Meta's error 131047
as `messaging.ErrOutsideWindow`, and implements `messaging.Reengager` to send an approved
template instead. The scheduler checks for that sentinel and falls back to a nudge rather than
dropping the monthly report silently. A template **cannot** carry the PDF — only the user
making contact reopens the window — so the nudge asks them to reply, and `/rekap` then serves
the report. Keep the sentinel in `messaging`, not the transport, so callers never import a
specific platform.

**The webhook is a public endpoint.** `whatsappcloud.Webhook` verifies Meta's
`X-Hub-Signature-256` HMAC on every POST and refuses everything when no app secret is set —
failing closed, because an unauthenticated endpoint here lets anyone write transactions into a
user's ledger. It also answers 200 before doing the work, since Meta retries anything slow and
a retry would double-record the transaction.

Keying off the **chat** ID, not the sender, means a group chat shares one spreadsheet.

The scheduler takes a `messaging.Sender` (the `Router`), not a whatsmeow client. Anything that
delivers to users must go through the router, or it will silently serve only one platform.

Telegram replies go through `telegram.ToHTML`, which converts the bot's WhatsApp-flavoured
`*bold*` / `_italic_` markup to Telegram HTML and escapes `& < >` first. Escaping must precede
markup conversion: Telegram rejects a message whose entities don't parse, so a stray `&` would
lose the reply entirely rather than just look wrong.

### Natural language is a translator, not a second execution path

`internal/ai` (Gemini) converts a free-form message or receipt photo into a structured
`ai.Intent`, and `Intent.Command()` renders that as a **canonical slash command** which is then
run through the same `handleCommand` dispatch as typed input. `Handle` splits the two cases;
`handleNaturalLanguage` does the parse-then-dispatch.

This boundary is the design, not an implementation detail — keep it:

- The model never touches the spreadsheet. Every rule (amount validation, wallet matching,
  balance bookkeeping, rollback) is enforced exactly once, on the command path, so a misread
  cannot bypass a constraint that typed input must satisfy.
- `Command()` returns `ok == false` for anything incomplete — no amount, no wallet, a transfer
  to the same wallet, a non-positive amount — and the caller **asks** rather than guessing. A
  guessed transaction writes silently to someone's ledger.
- Unrecognized chatter returns `ActionUnknown` with an **empty** `Reply`, and the handler stays
  silent. This is deliberate: the bot sits in group chats, where answering every message would
  be noise. A non-empty `Reply` means "looked financial but something's missing" — ask that.
- `handler.IntentParser` is an interface and `MessageHandler.parser` may be **nil**. With no
  `GEMINI_API_KEY` the bot runs exactly as before, slash commands only. Don't introduce a hard
  dependency on the parser being present.
- Infra errors are surfaced only when the message carried an image (a deliberate act); for
  plain prose they're logged and swallowed, again to avoid group-chat noise.

Two constraints on generated commands, both covered by `internal/ai/command_test.go`:
**category must be a single token** (the handler reads one word after the wallet as the category,
so a two-word category eats the description's first word), and **descriptions are collapsed to
one line** (commands are split with `strings.Fields`). Multi-word *wallet* names need no quoting
— `matchWalletPrefix` matches them greedily from the front.

`ai.Categories` is a closed vocabulary on purpose: the recap breakdown and the PDF chart only
group usefully when the same spending lands under the same label every time. Add to the list
rather than letting the model invent labels.

### Sheets client caching

`MessageHandler.cache` is keyed by **spreadsheet ID, not chat JID**, so several chats registered
to the same sheet reuse one client. Because of that, `/hapus` only evicts the cache entry after
`users.AnyUses(spreadsheetID)` confirms no other chat still points at it.

### Wallet balances are denormalized — respect the invariant

`Wallets!B` stores a running balance; it is never recomputed from the transaction history.
Every write path must keep the pair in sync, and `model.Transaction.BalanceDelta` is the one
place that decides a transaction's sign. Any undo must apply `-BalanceDelta()` *and* delete
the row.

There are no transactions across the Sheets API, so multi-step writes roll back by hand.
`sheets.AddTransaction` is the canonical shape, and its ordering is deliberate:

1. Resolve the wallet row **first** (`findWalletRow`), so an unknown wallet fails before
   anything is written.
2. Append the transaction row **before** updating the balance. If the balance write then
   fails, the sheet is left with a *visible* extra row rather than silent balance drift —
   the strictly more recoverable failure.
3. On a balance-write failure, roll the appended row back via `deleteRowIfLastWithID`,
   which refuses to delete a row that is no longer last (it would belong to another write).

When rollback itself fails, the returned error wraps **`sheets.ErrInconsistent`**, and
callers must `errors.Is` it and tell the user to check the sheet by hand instead of
reporting a plain failure. `cmdTransfer` and `undoTransfer` follow the same manual-rollback
pattern for their two-leg writes.

### Transfers are two linked rows

A `/transfer` writes two rows sharing a generated `RefID`: the `out` leg (source wallet) first,
then the `in` leg. `Direction` disambiguates them, since `Type == Transfer` alone cannot.
Because the out leg is written first, `/batal` detects a transfer by checking whether the row
*above* the last one carries the same `RefID`.

`recap.Compute` **excludes transfers entirely** — moving money between your own wallets is
neither income nor spending.

### Sheets row-index conventions

Data starts at row 2 because row 1 is the header, so a slice index maps to a sheet row as
`i + 2` (see `findWalletRow`). When deleting more than one row, delete the **higher index
first** so the lower one does not shift — `undoTransfer` depends on this.

`sheets.withRecovery` wraps most value calls: if the API returns the 400 "Unable to parse range"
error that means a required tab was deleted, it recreates the tabs via `InitSheets` and retries
once. New read/write helpers should be wrapped the same way.

### Dates

Transaction dates are written as local wall-clock strings (`2006-01-02 15:04:05`, no zone) and
must be parsed back with `time.ParseInLocation(..., time.Local)`. Plain `time.Parse` assumes UTC
and would silently shift every timestamp by the local offset. `internal/recap` builds all
`[from, to)` ranges — month, Monday-start week, day — in `now.Location()`, and ranges are always
half-open.

### Recap and reports share one computation

`internal/recap` is the shared core for both the text `/rekap` reply and the PDF, so period
logic and Indonesian month parsing/formatting live there and not in the handler.
`report.GenerateMonthly(title, txs, wallets)` returns raw PDF bytes, used by both the on-demand
`/rekap ... pdf` path and `internal/scheduler`, which fires on the 1st of each month at 07:00
local time (`fireDay`/`fireHour`) and sends the previous full month to every registered chat.

**The scheduler is opt-in** (`ENABLE_MONTHLY_REPORT`), and off by default. It is the only thing
the bot sends unprompted, which on the WhatsApp Cloud API means a billable template
conversation instead of a free reply inside the 24-hour window. `main.go` also mirrors the flag
onto `MessageHandler.MonthlyReports` so `/help` only promises the automatic report when it is
actually running — don't reintroduce that claim unconditionally. Everything the user asks for
directly, `/rekap ... pdf` included, is unaffected and free.

## Conventions

- **All user-facing strings are Indonesian**, with emoji prefixes and WhatsApp `*bold*` markup.
  Match that voice in new replies, and add the command to `helpMessage()` when you add one.
- Only messages beginning with `/` are handled; everything else returns a zero `Reply`.
- Amounts accept Indonesian shorthand via `parseAmount`: `50000`, `50rb`, `50ribu`, `1.5jt`,
  `1.5juta`, `200k`. Render them back with `model.FormatAmount`, which uses `.` as the
  thousands separator.
- Wallet names may contain spaces and are resolved by `matchWalletPrefix`, a greedy
  longest-prefix match against registered wallets. This is why command arguments are parsed
  positionally but variably — the wallet consumes as many leading tokens as it can, and
  whatever follows becomes `[kategori] [keterangan...]`.
- Wallet lookups are case-insensitive but resolve to the canonical stored name
  (`matchWalletName`), so what gets written to Sheets stays consistent.
- `handler.go` returns errors to users as formatted Indonesian strings rather than propagating
  them; internal detail goes to `log.Printf`.

## Gotchas

- The service-account address users must share their sheet with is hardcoded in the `/daftar`
  error message in `internal/handler/message.go`. It needs editing if the credentials change.
- `userstore.MigrateFromJSON` imports a legacy `users.json` on every startup and renames it to
  `users.json.migrated` so it is not re-imported. It is a no-op when absent.
- `internal/report`'s package comment says "bar chart", but `renderExpenseChart` actually
  renders a pie chart, capped at `maxChartBars` (6) slices with the remainder grouped.
- Secrets and local runtime state (`.env`, `credentials.json`, `gsm.json`, `users.json`,
  `whatsmeow.db*`) are gitignored — keep new runtime artifacts out of the repo too.
