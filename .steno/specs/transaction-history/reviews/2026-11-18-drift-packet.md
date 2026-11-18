# Review packet — transaction-history / drift

Reviewer: you have NO conversation history. Judge only what is below.

Compare the spec with the current code in the files listed. Report where they diverge.

## tasks.md
# Tasks — Transaction History

- [x] 1. Active transactions and the history core
  - [x] 1.1 Add `sheets.Client.GetActiveTransactions`: read through `readTransactions`, drop cancelled rows, keep Ledger order; add the cancelled-row case to `internal/sheets/live_test.go`. Verify: `go vet ./internal/sheets`, and `go test -tags live ./internal/sheets` against a blank sheet.
    - _Requirements: 5.1, 5.2_ _Design: D-data_
  - [x] 1.2 Create `internal/history` with `Query`, `Item`, `Result` and `Select`: group Transfer sides by RefID, filter by wallet and `[From, To)`, sort by Date then Ledger position, cut to Limit, set Total; table tests plus properties 1–3. Verify: `go test ./internal/history`.
    - _Requirements: 1.3, 2.4, 3.1, 4.2, 8.1_ _Design: D-select, D-prop-1, D-prop-2, D-prop-3_
  - [x] 1.3 Mark the undo target in `Select`: the last active transaction in Ledger order, the whole Item for a Transfer, only when the Item survives filters and the limit; property 4 and a filtered-out case. Verify: `go test ./internal/history`.
    - _Requirements: 6.1, 6.2, 6.3_ _Design: D-undo, D-prop-4_
- [x] 2. Checkpoint — history core end to end
  Verify: `go vet ./...`, `go test ./internal/history ./internal/sheets`; nothing user-visible changes yet.
- [x] 3. Shared period parser
  - [x] 3.1 Move `resolveMonthRange`, `resolveWeekRange` and `resolveDayRange` into `internal/handler/period.go` behind `parsePeriod(now, tokens) (*period, used, errMsg)`, validating the day against the month; switch `cmdRekap` to it with titles unchanged; regression tests of `/rekap` titles and ranges plus invalid dates such as 31 februari. Verify: `go test ./internal/handler -run Period`, then `/rekap`, `/rekap mingguan lalu`, `/rekap bulan januari 2025` give the same replies as before.
    - _Requirements: 8.3_ _Design: D-period_
  - [x] 3.2 Add the bare forms `TGL BULAN [TAHUN]` (a day) and `BULAN [TAHUN]` (a month) to `parsePeriod`, and return `used = 0` for a number not followed by a month name. Verify: `go test ./internal/handler -run Period` covering 5 januari, januari 2025 and a bare 15.
    - _Requirements: 8.4, 8.5_ _Design: D-period_
- [x] 4. The /riwayat command
  - [x] 4.1 Add `parseHistoryArgs` in `internal/handler/history_args.go`: whole-text wallet first, leading period before count, count 1–30 with the capped flag, period, then `matchWalletPrefix`; Format_Reply for bad counts and text after a matched wallet; table tests plus property 6. Verify: `go test ./internal/handler -run HistoryArgs`.
    - _Requirements: 1.1, 2.1, 2.3, 3.2, 3.5, 3.6_ _Design: D-args, D-prop-6, D-errors_
  - [x] 4.2 Render entries in `internal/handler/history_format.go`, with `history.FormatEntryDate`: one paragraph per Item, Pemasukan/Pengeluaran lines, Transfer `BCA → GoPay`, in/out wording under a wallet filter, full description with newlines as spaces, the undo line; golden-string tests. Verify: `go test ./internal/handler -run HistoryFormat ./internal/history`.
    - _Requirements: 1.2, 1.5, 4.1, 4.3, 7.1_ _Design: D-render_
  - [x] 4.3 Render the header (wallet, period label with dates), the "dan N transaksi lainnya" and capped footers, and the single empty reply with the recording hint only when unfiltered; golden-string tests. Verify: `go test ./internal/handler -run HistoryFormat`.
    - _Requirements: 2.2, 2.5, 3.4, 8.2, 5.3, 5.4_ _Design: D-frame, D-cmd_
  - [x] 4.4 Wire `cmdRiwayat` into `handleCommand` for `/riwayat` and `/history`: read wallets and active transactions, map them through `parseHistoryArgs`, `Select` and the formatters; the "Gagal memuat riwayat" reply on read errors; `walletNotFoundMsg` for unknown wallets; the `📜 Riwayat` block in `helpMessage()`. Verify: `go build ./cmd/api`, then on a test chat `/riwayat`, `/history 5`, `/riwayat GoPay`, `/riwayat Dana`, `/help`.
    - _Requirements: 1.4, 3.3, 5.5, 5.6, 7.2_ _Design: D-cmd, D-errors, D-help_
- [x] 5. Checkpoint — /riwayat end to end
  Verify: `go vet ./...`, `go test ./...`; on a registered test chat try `/riwayat`, `/riwayat 20 mingguan GoPay`, `/riwayat 5 januari`, `/riwayat 0`, `/riwayat BCA 5`, `/riwayat 31 feb`, then `/batal` and confirm the marked entry was the one cancelled; on an unregistered chat expect the `/daftar` instructions.
- [x] 6. Long replies
  - [x] 6.1 Add `messaging.SplitText(text, limit)` in `internal/messaging/split.go`: pack whole paragraphs, fall back to lines then runes only for an oversized paragraph; property 5 plus exactly-at-limit and single-huge-paragraph cases. Verify: `go test ./internal/messaging`.
    - _Requirements: 7.5_ _Design: D-split, D-prop-5_
  - [x] 6.2 Call `SplitText` from `SendText` in `internal/telegram/telegram.go` (4000, before `ToHTML`), `internal/whatsappcloud/client.go` (4000) and `internal/wasend/wasend.go` (60000), sending chunks in order and stopping at the first error. Verify: `go test ./internal/telegram ./internal/whatsappcloud`, and `go run ./cmd/tgsmoke` with a long message.
    - _Requirements: 7.5_ _Design: D-split_
- [x] 7. Checkpoint — long replies end to end
  Verify: `go test ./...`; on Telegram add a transaction with a very long description, then `/riwayat 30` arrives as several messages in order with no entry cut except the oversized one.
- [x] 8. Free-text history
  - [x] 8.1 Add `ActionHistory`, the `count` and `wallet_text` schema fields and a prompt line in `internal/ai/intent.go`; render `/riwayat [count] [period] [wallet]` in `Command()` with its own period mapping (this month → `bulanan`) and `wallet_text` when the wallet is "none"; `Mutates()` stays false; `command_test.go` cases. Verify: `go test ./internal/ai`, then `go run ./cmd/smoke` with "lihat transaksi gopay minggu ini" and "riwayat dompet Dana".
    - _Requirements: 7.3, 7.4_ _Design: D-ai_
- [x] 9. Checkpoint — final
  Verify: `go vet ./...`, `go test ./...`; on WhatsApp and Telegram: `/riwayat`, a filtered list, a free-text request, `/help`, and `/rekap bulan lalu` unchanged.


## requirements outline
R1 List recent transactions (5 criteria)
R2 Choose how many entries to show (5 criteria)
R3 Filter by wallet (6 criteria)
R4 Show each transfer once (3 criteria)
R5 Data availability and empty states (6 criteria)
R6 Undo awareness (3 criteria)
R7 Language, help and natural language (5 criteria)
R8 Filter by period (5 criteria)

## traceability
R1: design D-cmd,D-args,D-select,D-render,D-prop-1; tasks T1.2,T4.1,T4.2,T4.4
R2: design D-args,D-select,D-frame,D-prop-1,D-errors; tasks T1.2,T4.1,T4.3
R3: design D-args,D-select,D-frame,D-prop-3,D-prop-6,D-errors; tasks T1.2,T4.1,T4.3,T4.4
R4: design D-select,D-render,D-prop-2; tasks T1.2,T4.2
R5: design D-cmd,D-data,D-errors; tasks T1.1,T4.3,T4.4
R6: design D-undo,D-prop-4; tasks T1.3
R7: design D-render,D-split,D-help,D-ai,D-prop-5; tasks T4.2,T4.4,T6.1,T6.2,T8.1
R8: design D-period,D-select,D-frame,D-prop-3; tasks T1.2,T3.1,T3.2,T4.3

## referenced source files (read these)
internal/handler/message.go
internal/handler/history_args.go
internal/handler/period.go
internal/history/history.go
internal/handler/history_format.go
internal/messaging/split.go
internal/telegram/telegram.go
internal/whatsappcloud/client.go
internal/wasend/wasend.go
internal/ai/intent.go
internal/ai/command.go
internal/sheets/client.go
internal/history/history_test.go
internal/handler/history_format_test.go
internal/handler/history_args_test.go
internal/handler/period_test.go
internal/messaging/split_test.go
internal/ai/command_test.go
internal/sheets/live_test.go