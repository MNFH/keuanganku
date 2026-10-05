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

Only `internal/model` and `internal/handler` have tests so far, covering the pure
functions (`FormatAmount`, `BalanceDelta`, `parseAmount`). The other obvious candidates
— `recap.Compute`, the `*Range` helpers, `ParseIndoMonth`, `matchWalletPrefix`,
`parseTransactionRow`, `nextFireTime` — are equally dependency-free and still untested.

Note `gofmt -l .` lists every file in the repo: this is a Windows CRLF checkout and
gofmt expects LF. That is the pre-existing baseline, not a real formatting problem —
don't "fix" it by rewriting every file.

Running the bot requires `MYSQL_DSN` in `.env` (the process calls `log.Fatalf` if it is
missing) plus a Google service-account JSON at `GOOGLE_CREDENTIALS_FILE`
(default `credentials.json`). First run prints a QR code to link a WhatsApp account;
the session persists in `whatsmeow.db` so later restarts reconnect without rescanning.

Note `.env.example` lists two stale variables, `ANTHROPIC_API_KEY` and
`GOOGLE_SHEETS_SPREADSHEET_ID`, that `config.Load` never reads — only `MYSQL_DSN` and
`GOOGLE_CREDENTIALS_FILE` are real.

## Architecture

A WhatsApp chat bot for personal finance, driven entirely by slash commands in a chat.

### Three stores, and which data lives where

This split is the single most important thing to understand, and the README understates it
by calling Sheets an "optional export":

- **Google Sheets is the transaction database**, one spreadsheet per registered chat, with
  two tabs created and headed by `sheets.InitSheets`: `Wallets` (Name, Balance, Created At)
  and `Transactions` (ID, Date, Type, Amount, Wallet, Category, Description, RefID, Direction).
  All reads and writes of financial data go through `internal/sheets`.
- **MySQL holds only the `users` table** — a `chat JID → spreadsheet_id` mapping, auto-migrated
  on startup by `userstore.New`. The database itself must already exist; only the table is created.
- **SQLite (`whatsmeow.db`)** holds the WhatsApp device session, owned by whatsmeow's `sqlstore`.

### Message flow

`cmd/api/main.go` registers one whatsmeow event handler that filters messages (ignoring
anything empty, and ignoring the bot's own messages unless they start with `/`), then calls
`handler.Handle(ctx, chatJID, text)`. The handler **never sends anything** — it returns a
`handler.Reply` that is either `Text`, or a `Document` + `Filename` + `Caption`, or zero-value
meaning "send nothing". The caller decides how to deliver it. `internal/wasend` does document
uploads and is shared by `main.go` and the scheduler, which is the reason it exists as its own
package.

Keying off the **chat** JID, not the sender, means a group chat shares one spreadsheet.

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
