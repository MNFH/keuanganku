package sheets

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nurfaizh/keuanganku/internal/model"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

const (
	sheetWallets      = "Wallets"
	sheetTransactions = "Transactions"

	dateLayout = "2006-01-02 15:04:05"
)

// balanceFormula is what Wallets!B holds: the row's Opening Balance (D) plus
// every transaction on that wallet whose Cancelled column (Transactions!J) is
// empty. It finds its own row with ROW(), so the same text is correct on
// whichever row an append lands on.
//
// It must agree with model.Transaction.BalanceDelta, which EnsureBalanceFormulas
// uses to back out an opening balance.
const balanceFormula = `=INDEX($D:$D,ROW())` +
	`+SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"income",Transactions!$J:$J,"")` +
	`-SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"expense",Transactions!$J:$J,"")` +
	`+SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"transfer",Transactions!$I:$I,"in",Transactions!$J:$J,"")` +
	`-SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"transfer",Transactions!$I:$I,"out",Transactions!$J:$J,"")`

// ErrNoTransactions is returned when there is nothing to undo.
var ErrNoTransactions = errors.New("no transactions found")

// Client reads and writes one spreadsheet.
//
// Financial data is append-only. A transaction is only ever added; undoing
// one marks its Cancelled column instead of deleting it; and a wallet's
// balance is a formula over the Transactions tab that the bot reads but never
// writes. Every write is therefore a single API call — nothing to roll back,
// and two chats writing at once cannot lose each other's update.
type Client struct {
	svc           *sheets.Service
	spreadsheetID string

	formulasMu      sync.Mutex
	formulasChecked bool
}

func New(credentialsFile, spreadsheetID string) (*Client, error) {
	ctx := context.Background()
	svc, err := sheets.NewService(ctx, option.WithCredentialsFile(credentialsFile), option.WithScopes(sheets.SpreadsheetsScope))
	if err != nil {
		return nil, fmt.Errorf("create sheets service: %w", err)
	}
	return &Client{svc: svc, spreadsheetID: spreadsheetID}, nil
}

func (c *Client) InitSheets(ctx context.Context) error {
	resp, err := c.svc.Spreadsheets.Get(c.spreadsheetID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("get spreadsheet: %w", err)
	}

	existing := map[string]bool{}
	for _, s := range resp.Sheets {
		existing[s.Properties.Title] = true
	}

	var addRequests []*sheets.Request
	for _, name := range []string{sheetWallets, sheetTransactions} {
		if !existing[name] {
			addRequests = append(addRequests, &sheets.Request{
				AddSheet: &sheets.AddSheetRequest{
					Properties: &sheets.SheetProperties{Title: name},
				},
			})
		}
	}

	if len(addRequests) > 0 {
		_, err = c.svc.Spreadsheets.BatchUpdate(c.spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
			Requests: addRequests,
		}).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("create sheets: %w", err)
		}
	}

	// Write headers if sheets were just created — in one call, since a fresh
	// registration needs both and each round trip is a second or so.
	var headers []*sheets.ValueRange
	if !existing[sheetWallets] {
		headers = append(headers, headerRange(sheetWallets, "Name", "Balance", "Created At", "Opening Balance"))
	}
	if !existing[sheetTransactions] {
		headers = append(headers, headerRange(sheetTransactions, "ID", "Date", "Type", "Amount", "Wallet", "Category", "Description", "RefID", "Direction", "Cancelled"))
	}
	if len(headers) > 0 {
		_, err = c.svc.Spreadsheets.Values.BatchUpdate(c.spreadsheetID, &sheets.BatchUpdateValuesRequest{
			ValueInputOption: "RAW",
			Data:             headers,
		}).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("write headers: %w", err)
		}
	}

	return nil
}

func headerRange(sheet string, columns ...interface{}) *sheets.ValueRange {
	return &sheets.ValueRange{Range: sheet + "!A1", Values: [][]interface{}{columns}}
}

// EnsureBalanceFormulas converts every wallet whose Balance is a typed number
// — a sheet from before balances were formulas, or one overwritten by hand —
// to balanceFormula. It sets Opening Balance to the typed balance minus the
// wallet's active transactions, so the formula shows exactly what the row
// showed before. Wallets that already have a formula, including ones the user
// wrote, are left alone.
//
// It does the work once per Client. Callers must hold the sheet's lock and
// must not write transactions if it fails: a transaction recorded against a
// still-typed balance would be counted into the opening balance and vanish.
func (c *Client) EnsureBalanceFormulas(ctx context.Context) error {
	c.formulasMu.Lock()
	defer c.formulasMu.Unlock()
	if c.formulasChecked {
		return nil
	}

	resp, err := c.svc.Spreadsheets.Get(c.spreadsheetID).
		Ranges(sheetWallets + "!A1:D").
		IncludeGridData(true).
		Fields("sheets.data.rowData.values.userEnteredValue").
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("read wallet formulas: %w", err)
	}
	var rows []*sheets.RowData
	if len(resp.Sheets) > 0 && len(resp.Sheets[0].Data) > 0 {
		rows = resp.Sheets[0].Data[0].RowData
	}

	var updates []*sheets.ValueRange
	var txs []entry
	txsLoaded := false
	for i, rd := range rows {
		if i == 0 {
			continue // header
		}
		name := cellText(rd, 1)
		if name == "" {
			continue
		}
		balance := cellValue(rd, 2)
		if balance != nil && balance.FormulaValue != nil {
			continue
		}

		if !txsLoaded {
			if txs, err = c.readTransactions(ctx); err != nil {
				return err
			}
			txsLoaded = true
		}
		row := i + 1
		updates = append(updates,
			&sheets.ValueRange{Range: fmt.Sprintf("%s!B%d", sheetWallets, row), Values: [][]interface{}{{balanceFormula}}},
			&sheets.ValueRange{Range: fmt.Sprintf("%s!D%d", sheetWallets, row), Values: [][]interface{}{{openingBalance(typedNumber(balance), name, txs)}}},
		)
	}

	if len(updates) > 0 {
		if len(rows) > 0 && cellText(rows[0], 4) == "" {
			updates = append(updates, &sheets.ValueRange{Range: sheetWallets + "!D1", Values: [][]interface{}{{"Opening Balance"}}})
		}
		_, err = c.svc.Spreadsheets.Values.BatchUpdate(c.spreadsheetID, &sheets.BatchUpdateValuesRequest{
			ValueInputOption: "USER_ENTERED",
			Data:             updates,
		}).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("write balance formulas: %w", err)
		}
	}

	c.formulasChecked = true
	return nil
}

// openingBalance is the Opening Balance that makes balanceFormula reproduce
// balance: balance minus the wallet's active transactions. The wallet match is
// case-insensitive, like SUMIFS.
func openingBalance(balance float64, wallet string, txs []entry) float64 {
	for _, e := range txs {
		if !e.cancelled && strings.EqualFold(e.tx.Wallet, wallet) {
			balance -= e.tx.BalanceDelta()
		}
	}
	return balance
}

// cellValue returns the user-entered value of the 1-indexed column col, or nil.
func cellValue(rd *sheets.RowData, col int) *sheets.ExtendedValue {
	if rd == nil || len(rd.Values) < col || rd.Values[col-1] == nil {
		return nil
	}
	return rd.Values[col-1].UserEnteredValue
}

func cellText(rd *sheets.RowData, col int) string {
	v := cellValue(rd, col)
	switch {
	case v == nil:
		return ""
	case v.StringValue != nil:
		return strings.TrimSpace(*v.StringValue)
	case v.NumberValue != nil:
		return strconv.FormatFloat(*v.NumberValue, 'f', -1, 64)
	}
	return ""
}

// typedNumber reads a typed (non-formula) balance cell; blank counts as 0.
func typedNumber(v *sheets.ExtendedValue) float64 {
	if v == nil {
		return 0
	}
	if v.NumberValue != nil {
		return *v.NumberValue
	}
	if v.StringValue != nil {
		n, _ := strconv.ParseFloat(strings.TrimSpace(*v.StringValue), 64)
		return n
	}
	return 0
}

// isMissingSheetError reports whether err is the Sheets API's "Unable to
// parse range" error, which happens when a referenced tab has been deleted.
func isMissingSheetError(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == 400 && strings.Contains(gerr.Message, "Unable to parse range")
	}
	return false
}

// withRecovery runs fn, and if it fails because a required tab (Wallets or
// Transactions) was deleted from the spreadsheet, recreates any missing tabs
// via InitSheets and retries fn once.
func (c *Client) withRecovery(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil || !isMissingSheetError(err) {
		return err
	}
	if recErr := c.InitSheets(ctx); recErr != nil {
		return err
	}
	return fn()
}

// GetWallets returns every wallet with its balance as the sheet computes it.
func (c *Client) GetWallets(ctx context.Context) ([]model.Wallet, error) {
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		// Unformatted, so a currency format on the Balance column still
		// parses as a number.
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, sheetWallets+"!A2:B").
			ValueRenderOption("UNFORMATTED_VALUE").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return nil, fmt.Errorf("get wallets: %w", err)
	}

	var wallets []model.Wallet
	for _, row := range resp.Values {
		if len(row) < 1 || fmt.Sprint(row[0]) == "" {
			continue
		}
		var balance float64
		if len(row) >= 2 {
			balance, _ = strconv.ParseFloat(fmt.Sprint(row[1]), 64)
		}
		wallets = append(wallets, model.Wallet{
			Name:    fmt.Sprint(row[0]),
			Balance: balance,
		})
	}
	return wallets, nil
}

// AddWallet appends a wallet with an opening balance of 0 and the balance
// formula, in one write.
func (c *Client) AddWallet(ctx context.Context, name string) error {
	if err := validWalletName(name); err != nil {
		return err
	}
	wallets, err := c.GetWallets(ctx)
	if err != nil {
		return err
	}
	for _, w := range wallets {
		if strings.EqualFold(w.Name, name) {
			return fmt.Errorf("wallet %q already exists", w.Name)
		}
	}
	// USER_ENTERED so the formula is evaluated; the leading apostrophes keep
	// the name and timestamp as literal text rather than letting Sheets parse
	// them (a name starting with "=" would otherwise become a formula).
	return c.appendRows(ctx, sheetWallets+"!A:D", "USER_ENTERED", [][]interface{}{
		{"'" + name, balanceFormula, "'" + time.Now().Format(dateLayout), 0},
	})
}

// validWalletName rejects names balanceFormula cannot match. SUMIFS reads its
// criterion as a pattern: a leading = < > makes it a comparison, and * ? ~
// are wildcards anywhere, so such a wallet would never count its own
// transactions. Requiring a letter or digit first rules out every operator
// at once; real wallet names (bank, e-wallet) never start with a symbol.
func validWalletName(name string) error {
	first, _ := utf8.DecodeRuneInString(name)
	if name == "" || !(unicode.IsLetter(first) || unicode.IsDigit(first)) || strings.ContainsAny(name, "*?~") {
		return fmt.Errorf("nama dompet harus diawali huruf atau angka, dan tidak boleh berisi * ? ~")
	}
	return nil
}

// AddTransactions records txs in a single append, so the two legs of a
// transfer land together or not at all. Every wallet must already exist; the
// check comes first so an unknown wallet writes nothing.
func (c *Client) AddTransactions(ctx context.Context, txs ...model.Transaction) error {
	wallets, err := c.GetWallets(ctx)
	if err != nil {
		return err
	}
	rows := make([][]interface{}, 0, len(txs))
	for _, tx := range txs {
		if !hasWallet(wallets, tx.Wallet) {
			return fmt.Errorf("wallet %q not found", tx.Wallet)
		}
		rows = append(rows, []interface{}{
			tx.ID,
			tx.Date.Format(dateLayout),
			string(tx.Type),
			tx.Amount,
			tx.Wallet,
			tx.Category,
			tx.Description,
			tx.RefID,
			string(tx.Direction),
			"",
		})
	}
	return c.appendRows(ctx, sheetTransactions+"!A:J", "RAW", rows)
}

func hasWallet(wallets []model.Wallet, name string) bool {
	for _, w := range wallets {
		if w.Name == name {
			return true
		}
	}
	return false
}

// CancelLast marks the most recent active transaction as cancelled — both
// legs, if it is a transfer — by writing note into its Cancelled column. The
// rows stay in the sheet as history; the balance formula and recaps skip
// them. It returns what it cancelled, or ErrNoTransactions.
func (c *Client) CancelLast(ctx context.Context, note string) ([]model.Transaction, error) {
	entries, err := c.readTransactions(ctx)
	if err != nil {
		return nil, err
	}
	targets := lastActive(entries)
	if len(targets) == 0 {
		return nil, ErrNoTransactions
	}

	data := make([]*sheets.ValueRange, len(targets))
	cancelled := make([]model.Transaction, len(targets))
	for i, e := range targets {
		data[i] = &sheets.ValueRange{
			Range:  fmt.Sprintf("%s!J%d", sheetTransactions, e.row),
			Values: [][]interface{}{{note}},
		}
		cancelled[i] = e.tx
	}
	_, err = c.svc.Spreadsheets.Values.BatchUpdate(c.spreadsheetID, &sheets.BatchUpdateValuesRequest{
		ValueInputOption: "RAW",
		Data:             data,
	}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("mark cancelled: %w", err)
	}
	return cancelled, nil
}

// lastActive returns the last non-cancelled entry, followed by the other
// active leg sharing its RefID if it is a transfer. Empty when nothing is
// left to cancel.
func lastActive(entries []entry) []entry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].cancelled {
			continue
		}
		targets := []entry{entries[i]}
		if ref := entries[i].tx.RefID; ref != "" {
			for j, e := range entries {
				if j != i && !e.cancelled && e.tx.RefID == ref {
					targets = append(targets, e)
				}
			}
		}
		return targets
	}
	return nil
}

// entry is a transaction with where it sits in the sheet.
type entry struct {
	tx        model.Transaction
	row       int // 1-indexed sheet row
	cancelled bool
}

// readTransactions returns every well-formed transaction row, in sheet order.
func (c *Client) readTransactions(ctx context.Context) ([]entry, error) {
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, sheetTransactions+"!A2:J").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return nil, fmt.Errorf("get transactions: %w", err)
	}

	var entries []entry
	for i, row := range resp.Values {
		tx, err := parseTransactionRow(row)
		if err != nil {
			continue // skip malformed rows rather than failing the whole read
		}
		entries = append(entries, entry{
			tx:        tx,
			row:       i + 2, // data starts at row 2
			cancelled: len(row) >= 10 && strings.TrimSpace(fmt.Sprint(row[9])) != "",
		})
	}
	return entries, nil
}

// GetActiveTransactions returns every active (not cancelled) transaction in
// Ledger order, i.e. the order the rows were appended. That order matters:
// its last element is exactly what CancelLast would cancel, so callers can
// show the /batal target from the same read.
func (c *Client) GetActiveTransactions(ctx context.Context) ([]model.Transaction, error) {
	entries, err := c.readTransactions(ctx)
	if err != nil {
		return nil, err
	}
	return activeTransactions(entries), nil
}

// activeTransactions drops cancelled entries, keeping sheet order.
func activeTransactions(entries []entry) []model.Transaction {
	var result []model.Transaction
	for _, e := range entries {
		if !e.cancelled {
			result = append(result, e.tx)
		}
	}
	return result
}

// GetTransactionsInRange returns all active (not cancelled) transactions with
// Date in [from, to).
func (c *Client) GetTransactionsInRange(ctx context.Context, from, to time.Time) ([]model.Transaction, error) {
	entries, err := c.readTransactions(ctx)
	if err != nil {
		return nil, err
	}

	var result []model.Transaction
	for _, e := range entries {
		if e.cancelled || e.tx.Date.Before(from) || !e.tx.Date.Before(to) {
			continue
		}
		result = append(result, e.tx)
	}
	return result, nil
}

func parseTransactionRow(row []interface{}) (model.Transaction, error) {
	if len(row) < 7 {
		return model.Transaction{}, fmt.Errorf("malformed transaction row")
	}
	amount, err := strconv.ParseFloat(fmt.Sprint(row[3]), 64)
	if err != nil {
		return model.Transaction{}, fmt.Errorf("parse amount: %w", err)
	}
	// Dates are stored as local wall-clock time (see AddTransactions), so they
	// must be parsed back in Local — time.Parse defaults to UTC for a layout
	// with no zone, which would silently shift every timestamp by the local
	// UTC offset.
	date, _ := time.ParseInLocation(dateLayout, fmt.Sprint(row[1]), time.Local)
	refID := ""
	if len(row) >= 8 {
		refID = fmt.Sprint(row[7])
	}
	direction := model.Direction("")
	if len(row) >= 9 {
		direction = model.Direction(fmt.Sprint(row[8]))
	}
	return model.Transaction{
		ID:          fmt.Sprint(row[0]),
		Date:        date,
		Type:        model.TransactionType(fmt.Sprint(row[2])),
		Amount:      amount,
		Wallet:      fmt.Sprint(row[4]),
		Category:    fmt.Sprint(row[5]),
		Description: fmt.Sprint(row[6]),
		RefID:       refID,
		Direction:   direction,
	}, nil
}

func (c *Client) GetSummary(ctx context.Context) (string, error) {
	wallets, err := c.GetWallets(ctx)
	if err != nil {
		return "", err
	}
	if len(wallets) == 0 {
		return "No wallets found. Add one with: tambah dompet <nama>", nil
	}

	summary := "💰 *Ringkasan Keuangan*\n\n"
	var total float64
	for _, w := range wallets {
		summary += fmt.Sprintf("• *%s*: Rp %s\n", w.Name, model.FormatAmount(w.Balance))
		total += w.Balance
	}
	summary += fmt.Sprintf("\n*Total: Rp %s*", model.FormatAmount(total))
	return summary, nil
}

func (c *Client) appendRows(ctx context.Context, rangeStr, inputOption string, rows [][]interface{}) error {
	return c.withRecovery(ctx, func() error {
		_, err := c.svc.Spreadsheets.Values.Append(c.spreadsheetID, rangeStr,
			&sheets.ValueRange{Values: rows}).
			ValueInputOption(inputOption).InsertDataOption("INSERT_ROWS").Context(ctx).Do()
		return err
	})
}
