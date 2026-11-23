# Tasks — Daily Breakdown in PDF Report

- [x] 1. Daily aggregation core
  - [x] 1.1 Add `DailyTotal`, `Net()` and `Daily(txs, from, to)` to `internal/recap/recap.go`: pre-fill one zero entry per day stepping with `AddDate(0,0,1)`, bucket Income/Expense by local year-month-day, skip Transfers and out-of-range dates, return empty for `from >= to`. Create `internal/recap/recap_test.go` with `testing/quick` tests for properties 1–4 plus table cases (28/30/31-day months, a Mon–Sun week, a DST-zone month, transfer-only input, a date outside the range). Verify: `go test ./internal/recap`.
    - _Requirements: 1.2, 2.1, 2.2, 2.3, 2.4, 3.2, 3.3_ _Design: D-daily, D-prop-1, D-prop-2, D-prop-3, D-prop-4, D-data, D-errors_
  - [x] 1.2 Add `IndoWeekday` and `FormatDayShort` ("Senin, 3 Nov", no year) to `internal/recap/recap.go`, with a table test over all seven weekdays and twelve month abbreviations. Verify: `go test ./internal/recap -run 'Weekday|DayShort'`.
    - _Requirements: 5.2_ _Design: D-indo-labels_
- [x] 2. Checkpoint — recap core end to end
  Verify: `go vet ./...`, `go test ./internal/recap`. Nothing user-visible changes yet.
- [ ] 3. Daily breakdown in the PDF
  - [x] 3.1 Change `report.GenerateMonthly` to take `from, to time.Time` after `title`, and pass the existing `from, to` from `renderRekapPDF` in `internal/handler/message.go` and `sendReportFor` in `internal/scheduler/scheduler.go`. Both keep fetching through `GetTransactionsInRange`, so cancelled rows stay excluded. The PDF output stays the same, and `renderRekap`/`formatRecap` stay untouched. Verify: `go build ./cmd/api`, `go vet ./...`, `go test ./internal/handler ./internal/scheduler`.
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 3.4_ _Design: D-callers, D-report-layout, D-data_
  - [x] 3.2 Add `drawDailyTable` in `internal/report/report.go`: heading "Rincian Harian", columns Tanggal/Pemasukan/Pengeluaran/Selisih, `FormatDayShort` dates, `Rp` amounts, red/green/black difference cell, bold "Total" row summed from the rows, and no other highlighting. Call it after `drawWalletTable` only when `len(days) > 1`, with no chart. Verify: `go build ./cmd/api`, then `/rekap mingguan pdf` shows the table last and `/rekap hari ini pdf` shows none.
    - _Requirements: 1.1, 1.3, 1.4, 1.6, 3.1, 5.1, 5.3, 5.4, 5.5_ _Design: D-daily-table, D-report-layout, D-indo-labels_
  - [ ] 3.3 Add page-break handling in `drawDailyTable`: before each row, if it would cross the bottom margin, `AddPage` and redraw the column header. Keep the heading, header and first row together. Create `internal/report/report_test.go`: a single-day and a 31-day empty period both return bytes starting `%PDF`, and the 31-day document has more pages (via an unexported builder returning the `*gofpdf.Fpdf`). Verify: `go test ./internal/report`.
    - _Requirements: 1.5, 4.5_ _Design: D-daily-table, D-report-layout_
- [ ] 4. Checkpoint — final
  Verify: `go vet ./...`, `go test ./...`. On a registered test chat:
  - `/rekap pdf` (this month): the daily table is the last section, spans pages with headings repeated, lists every day, and its total row matches the summary at the top.
  - `/rekap mingguan lalu pdf`: 7 rows, Senin first.
  - `/rekap hari ini pdf`: no daily table.
  - `/batal` a transaction, then regenerate: the transaction disappears from its day.
  - `/rekap` (text): unchanged.
  - With `ENABLE_WEEKLY_REPORT` on, the scheduled PDF includes the table.
