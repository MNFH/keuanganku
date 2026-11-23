---
inclusion: fileMatch
fileMatchPattern: "internal/sheets/**"
---
# Ledger

`internal/sheets/client.go` is the only code that touches a user's spreadsheet. The mistake agents make most often is computing a balance in Go and writing it back. Balances are a sheet formula, and the ledger is append-only.

## Purpose
- `InitSheets` creates two tabs. `Wallets` has columns A Name, B Balance, C Created At and D Opening Balance. `Transactions` has A ID, B Date, C Type, D Amount, E Wallet, F Category, G Description, H RefID, I Direction and J Cancelled.
- `Wallets!B` is `balanceFormula`: D plus a SUMIFS over the wallet's transactions where J is empty. It locates its own row with `INDEX(..,ROW())`.
- Sheet row = slice index + 2, because row 1 is the header (see `readTransactions`).

## Rules
- `AddTransactions` checks that every wallet exists, then appends all rows in one call, so both legs of a transfer land together.
- `CancelLast` (used by `/batal`) writes `batal <time>` into column J of the last active row, and of its transfer pair matched by `RefID` anywhere in the sheet, in one batch. Rows are never deleted.
- `GetTransactionsInRange` skips cancelled rows. Recaps and PDFs rely on that.
- `AddWallet` appends with `USER_ENTERED`, opening balance 0, and the name and timestamp prefixed with `'` so they stay text.
- `validWalletName` rejects a leading non-alphanumeric character and `* ? ~`, because SUMIFS reads the criterion as a pattern.
- `EnsureBalanceFormulas` converts legacy typed balances once per client: opening = typed balance − active transactions. User-written formulas are left alone.
- `balanceFormula` and `model.Transaction.BalanceDelta` must agree on every sign. `TestBalanceFormulaMatchesBalanceDelta` and `TestOpeningBalance` guard this.

## Patterns to reuse
- Wrap every values call in `withRecovery`. It recreates deleted tabs on the "Unable to parse range" 400 error and retries once.
- Use `appendRows(ctx, range, inputOption, rows)` for writes. `lastActive` finds the undo target. `parseTransactionRow` decodes a row.
- Parse dates with `time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)`.

## Do not
- Do not write `Wallets!B` or add a read-modify-write of a balance. Several chats share one sheet.
- Do not delete or reorder rows. Row numbers must stay valid for the life of a command.
- Do not run `live_test.go` (`-tags live`) against a real ledger. Use a blank sheet shared with the service account.
