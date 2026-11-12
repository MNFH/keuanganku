package sheets

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

const (
	sheetWallets      = "Wallets"
	sheetTransactions = "Transactions"

	// txRange covers every Transactions column, J (Canceled At) included.
	txRange = sheetTransactions + "!A2:J"

	// canceledHeader titles column J, which holds the time a transaction was
	// soft-deleted by /batal. Empty means the transaction is active.
	canceledHeader = "Canceled At"
)

// transactionHeaders is the Transactions tab's header row.
var transactionHeaders = []interface{}{"ID", "Date", "Type", "Amount", "Wallet", "Category", "Description", "RefID", "Direction", canceledHeader}

// ErrNoTransactions is returned when there is nothing to undo.
var ErrNoTransactions = errors.New("no transactions found")

// ErrInconsistent is wrapped into errors returned when a multi-step write
// failed partway and could not be rolled back, so wallet balances and
// transaction rows no longer agree and the sheet needs manual review.
// Callers must surface this to the user rather than reporting a plain failure.
var ErrInconsistent = errors.New("spreadsheet left inconsistent")

type Client struct {
	svc           *sheets.Service
	spreadsheetID string

	// formulaMu serialises wallet-formula migration; formulasReady skips it
	// once it has succeeded for this client (see ensureWalletFormulas).
	formulaMu     sync.Mutex
	formulasReady atomic.Bool
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
	resp, err := c.svc.Spreadsheets.Get(c.spreadsheetID).Do()
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

	// Write headers if sheets were just created
	if !existing[sheetWallets] {
		if err := c.writeRow(ctx, sheetWallets+"!A1", walletHeaders); err != nil {
			return err
		}
	}
	if !existing[sheetTransactions] {
		if err := c.writeRow(ctx, sheetTransactions+"!A1", transactionHeaders); err != nil {
			return err
		}
	}

	return nil
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
	// A recreated tab breaks the balance formulas (#REF!); re-check them on
	// the next GetWallets.
	c.formulasReady.Store(false)
	return fn()
}

// AddTransaction records tx by appending its row. That is the whole write:
// the wallet's balance is a formula over the Transactions tab (see
// wallets.go), so there is no second write to keep in sync or roll back.
//
// The wallet is checked first so an unknown wallet fails before anything is
// written — the formula would otherwise silently ignore the row.
func (c *Client) AddTransaction(ctx context.Context, tx model.Transaction) error {
	if err := c.walletExists(ctx, tx.Wallet); err != nil {
		return err
	}

	return c.appendRow(ctx, sheetTransactions+"!A:I", []interface{}{
		tx.ID,
		tx.Date.Format("2006-01-02 15:04:05"),
		string(tx.Type),
		tx.Amount,
		tx.Wallet,
		tx.Category,
		tx.Description,
		tx.RefID,
		string(tx.Direction),
	})
}

// RemoveIfLast hard-deletes the final transaction row if it still carries the
// given ID, rolling back a multi-row write (a transfer's first leg) whose
// next step failed — that transaction never happened, so it is removed
// rather than marked canceled. It refuses to delete a row that is no longer
// the last one, since that row belongs to some other write.
func (c *Client) RemoveIfLast(ctx context.Context, id string) error {
	last, rowNumber, err := c.GetLastTransaction(ctx)
	if err != nil {
		return fmt.Errorf("locate appended row: %w", err)
	}
	if last.ID != id {
		return fmt.Errorf("appended row is no longer the last row")
	}
	return c.DeleteTransactionRow(ctx, rowNumber)
}

// readTransactionRows returns every raw data row of the Transactions tab;
// index i is sheet row i+2.
func (c *Client) readTransactionRows(ctx context.Context) ([][]interface{}, error) {
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, txRange).Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return nil, fmt.Errorf("get transactions: %w", err)
	}
	return resp.Values, nil
}

// TxRow is a transaction together with its 1-indexed sheet row.
type TxRow struct {
	Tx  model.Transaction
	Row int
}

// LastActiveEntry returns what /batal should undo next: the most recent
// transaction that has not already been canceled, plus the other leg when it
// is a transfer. Canceled rows are skipped, so repeated /batal walks back
// through history one entry at a time instead of hitting the same row again.
// Returns ErrNoTransactions when nothing is left to undo.
func (c *Client) LastActiveEntry(ctx context.Context) ([]TxRow, error) {
	rows, err := c.readTransactionRows(ctx)
	if err != nil {
		return nil, err
	}
	txs := make([]*model.Transaction, len(rows))
	for i, row := range rows {
		if tx, perr := parseTransactionRow(row); perr == nil {
			txs[i] = &tx
		}
	}
	idxs := lastActiveEntry(txs)
	if len(idxs) == 0 {
		return nil, ErrNoTransactions
	}
	out := make([]TxRow, len(idxs))
	for i, idx := range idxs {
		out[i] = TxRow{Tx: *txs[idx], Row: idx + 2}
	}
	return out, nil
}

// lastActiveEntry picks the slice indices /batal should undo. txs[i] is nil
// for a malformed row, which is never a candidate. For a transfer, the other
// leg is the nearest active row above that shares its RefID — normally the
// row directly above, since the out leg is written first.
func lastActiveEntry(txs []*model.Transaction) []int {
	for i := len(txs) - 1; i >= 0; i-- {
		tx := txs[i]
		if tx == nil || tx.Canceled {
			continue
		}
		if tx.RefID == "" {
			return []int{i}
		}
		for j := i - 1; j >= 0; j-- {
			if p := txs[j]; p != nil && !p.Canceled && p.RefID == tx.RefID {
				return []int{i, j}
			}
		}
		// The other leg is missing or already canceled (a partly-failed
		// write, or a hand edit): undo the leg we have.
		return []int{i}
	}
	return nil
}

// SetCanceled marks (or, with canceled false, unmarks) the given sheet rows as
// soft-deleted, in a single API call so a transfer's two legs change together.
// It also (re)writes the column header, so sheets created before column J
// existed get it on their first cancellation.
func (c *Client) SetCanceled(ctx context.Context, rows []int, canceled bool, at time.Time) error {
	value := ""
	if canceled {
		value = at.Format("2006-01-02 15:04:05")
	}
	data := []*sheets.ValueRange{{
		Range:  sheetTransactions + "!J1",
		Values: [][]interface{}{{canceledHeader}},
	}}
	for _, r := range rows {
		data = append(data, &sheets.ValueRange{
			Range:  fmt.Sprintf("%s!J%d", sheetTransactions, r),
			Values: [][]interface{}{{value}},
		})
	}
	if err := c.batchWrite(ctx, data, "RAW"); err != nil {
		return fmt.Errorf("mark transaction canceled: %w", err)
	}
	return nil
}

// GetLastTransaction returns the most recently appended row, canceled or not,
// and its 1-indexed sheet row, or ErrNoTransactions if the sheet is empty. It
// exists for rolling back a just-appended write; /batal uses LastActiveEntry.
func (c *Client) GetLastTransaction(ctx context.Context) (model.Transaction, int, error) {
	rows, err := c.readTransactionRows(ctx)
	if err != nil {
		return model.Transaction{}, -1, err
	}
	if len(rows) == 0 {
		return model.Transaction{}, -1, ErrNoTransactions
	}

	rowNumber := len(rows) + 1 // data starts at row 2, so last row = count + 1
	tx, err := parseTransactionRow(rows[len(rows)-1])
	if err != nil {
		return model.Transaction{}, -1, err
	}
	return tx, rowNumber, nil
}

// GetTransactionsInRange returns all active (not canceled) transactions with
// Date in [from, to). Canceled rows are excluded because their effect has
// already been reversed out of the wallet balances.
func (c *Client) GetTransactionsInRange(ctx context.Context, from, to time.Time) ([]model.Transaction, error) {
	rows, err := c.readTransactionRows(ctx)
	if err != nil {
		return nil, err
	}

	var result []model.Transaction
	for _, row := range rows {
		tx, err := parseTransactionRow(row)
		if err != nil {
			continue // skip malformed rows rather than failing the whole recap
		}
		if tx.Canceled {
			continue
		}
		if tx.Date.Before(from) || !tx.Date.Before(to) {
			continue
		}
		result = append(result, tx)
	}
	return result, nil
}

// DeleteTransactionRow removes a single row (1-indexed) from the Transactions sheet.
func (c *Client) DeleteTransactionRow(ctx context.Context, rowNumber int) error {
	sheetID, err := c.getSheetID(ctx, sheetTransactions)
	if err != nil {
		// Tab may have been deleted — recreate it and retry once.
		if recErr := c.InitSheets(ctx); recErr != nil {
			return err
		}
		sheetID, err = c.getSheetID(ctx, sheetTransactions)
		if err != nil {
			return err
		}
	}

	_, err = c.svc.Spreadsheets.BatchUpdate(c.spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				DeleteDimension: &sheets.DeleteDimensionRequest{
					Range: &sheets.DimensionRange{
						SheetId:    sheetID,
						Dimension:  "ROWS",
						StartIndex: int64(rowNumber - 1),
						EndIndex:   int64(rowNumber),
					},
				},
			},
		},
	}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("delete transaction row: %w", err)
	}
	return nil
}

func (c *Client) getSheetID(ctx context.Context, title string) (int64, error) {
	resp, err := c.svc.Spreadsheets.Get(c.spreadsheetID).Context(ctx).Do()
	if err != nil {
		return 0, fmt.Errorf("get spreadsheet: %w", err)
	}
	for _, s := range resp.Sheets {
		if s.Properties.Title == title {
			return s.Properties.SheetId, nil
		}
	}
	return 0, fmt.Errorf("sheet %q not found", title)
}

func parseTransactionRow(row []interface{}) (model.Transaction, error) {
	if len(row) < 7 {
		return model.Transaction{}, fmt.Errorf("malformed transaction row")
	}
	amount, err := strconv.ParseFloat(fmt.Sprint(row[3]), 64)
	if err != nil {
		return model.Transaction{}, fmt.Errorf("parse amount: %w", err)
	}
	// Dates are stored as local wall-clock time (see AddTransaction), so they
	// must be parsed back in Local — time.Parse defaults to UTC for a layout
	// with no zone, which would silently shift every timestamp by the local
	// UTC offset.
	date, _ := time.ParseInLocation("2006-01-02 15:04:05", fmt.Sprint(row[1]), time.Local)
	refID := ""
	if len(row) >= 8 {
		refID = fmt.Sprint(row[7])
	}
	direction := model.Direction("")
	if len(row) >= 9 {
		direction = model.Direction(fmt.Sprint(row[8]))
	}
	// Any non-empty value in column J counts as canceled, so a mark typed by
	// hand in the sheet is honoured too.
	canceled := len(row) >= 10 && strings.TrimSpace(fmt.Sprint(row[9])) != ""
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
		Canceled:    canceled,
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

func (c *Client) writeRow(ctx context.Context, rangeStr string, values []interface{}) error {
	_, err := c.svc.Spreadsheets.Values.Update(c.spreadsheetID, rangeStr,
		&sheets.ValueRange{Values: [][]interface{}{values}}).
		ValueInputOption("RAW").Context(ctx).Do()
	return err
}

func (c *Client) appendRow(ctx context.Context, rangeStr string, values []interface{}) error {
	return c.withRecovery(ctx, func() error {
		_, err := c.svc.Spreadsheets.Values.Append(c.spreadsheetID, rangeStr,
			&sheets.ValueRange{Values: [][]interface{}{values}}).
			ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Context(ctx).Do()
		return err
	})
}
