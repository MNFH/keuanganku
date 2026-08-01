package sheets

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

const (
	sheetWallets      = "Wallets"
	sheetTransactions = "Transactions"
)

// ErrNoTransactions is returned when there is nothing to undo.
var ErrNoTransactions = errors.New("no transactions found")

type Client struct {
	svc           *sheets.Service
	spreadsheetID string
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
		if err := c.writeRow(ctx, sheetWallets+"!A1", []interface{}{"Name", "Balance", "Created At"}); err != nil {
			return err
		}
	}
	if !existing[sheetTransactions] {
		if err := c.writeRow(ctx, sheetTransactions+"!A1", []interface{}{"ID", "Date", "Type", "Amount", "Wallet", "Category", "Description", "RefID", "Direction"}); err != nil {
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
	return fn()
}

func (c *Client) GetWallets(ctx context.Context) ([]model.Wallet, error) {
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, sheetWallets+"!A2:B").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return nil, fmt.Errorf("get wallets: %w", err)
	}

	var wallets []model.Wallet
	for _, row := range resp.Values {
		if len(row) < 2 {
			continue
		}
		balance, _ := strconv.ParseFloat(fmt.Sprint(row[1]), 64)
		wallets = append(wallets, model.Wallet{
			Name:    fmt.Sprint(row[0]),
			Balance: balance,
		})
	}
	return wallets, nil
}

func (c *Client) AddWallet(ctx context.Context, name string) error {
	wallets, err := c.GetWallets(ctx)
	if err != nil {
		return err
	}
	for _, w := range wallets {
		if w.Name == name {
			return fmt.Errorf("wallet %q already exists", name)
		}
	}
	return c.appendRow(ctx, sheetWallets+"!A:C", []interface{}{name, 0, time.Now().Format("2006-01-02 15:04:05")})
}

func (c *Client) AddTransaction(ctx context.Context, tx model.Transaction) error {
	if err := c.AdjustWalletBalance(ctx, tx.Wallet, tx.BalanceDelta()); err != nil {
		return err
	}

	// Append transaction row
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

// AdjustWalletBalance adds delta (positive or negative) to a wallet's balance.
func (c *Client) AdjustWalletBalance(ctx context.Context, walletName string, delta float64) error {
	row, currentBalance, err := c.findWalletRow(ctx, walletName)
	if err != nil {
		return err
	}

	balanceRange := fmt.Sprintf("%s!B%d", sheetWallets, row)
	err = c.withRecovery(ctx, func() error {
		_, innerErr := c.svc.Spreadsheets.Values.Update(c.spreadsheetID, balanceRange,
			&sheets.ValueRange{Values: [][]interface{}{{currentBalance + delta}}}).
			ValueInputOption("RAW").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return fmt.Errorf("update wallet balance: %w", err)
	}
	return nil
}

// findWalletRow returns the 1-indexed sheet row and current balance of a wallet.
func (c *Client) findWalletRow(ctx context.Context, walletName string) (row int, balance float64, err error) {
	wallets, err := c.GetWallets(ctx)
	if err != nil {
		return -1, 0, err
	}
	for i, w := range wallets {
		if w.Name == walletName {
			return i + 2, w.Balance, nil // +2 because row 1 is header, rows are 1-indexed
		}
	}
	return -1, 0, fmt.Errorf("wallet %q not found", walletName)
}

// GetLastTransaction returns the most recently appended transaction and its
// 1-indexed sheet row, or ErrNoTransactions if the sheet is empty.
func (c *Client) GetLastTransaction(ctx context.Context) (model.Transaction, int, error) {
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, sheetTransactions+"!A2:I").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return model.Transaction{}, -1, fmt.Errorf("get transactions: %w", err)
	}
	if len(resp.Values) == 0 {
		return model.Transaction{}, -1, ErrNoTransactions
	}

	row := resp.Values[len(resp.Values)-1]
	rowNumber := len(resp.Values) + 1 // data starts at row 2, so last row = count + 1

	tx, err := parseTransactionRow(row)
	if err != nil {
		return model.Transaction{}, -1, err
	}
	return tx, rowNumber, nil
}

// GetTransactionsInRange returns all transactions with Date in [from, to).
func (c *Client) GetTransactionsInRange(ctx context.Context, from, to time.Time) ([]model.Transaction, error) {
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, sheetTransactions+"!A2:I").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return nil, fmt.Errorf("get transactions: %w", err)
	}

	var result []model.Transaction
	for _, row := range resp.Values {
		tx, err := parseTransactionRow(row)
		if err != nil {
			continue // skip malformed rows rather than failing the whole recap
		}
		if tx.Date.Before(from) || !tx.Date.Before(to) {
			continue
		}
		result = append(result, tx)
	}
	return result, nil
}

// GetTransactionRow returns the transaction stored at a specific 1-indexed
// sheet row, or ErrNoTransactions if that row has no data (e.g. row 1 is the
// header, or the row is out of range).
func (c *Client) GetTransactionRow(ctx context.Context, rowNumber int) (model.Transaction, error) {
	if rowNumber < 2 {
		return model.Transaction{}, ErrNoTransactions
	}
	rangeStr := fmt.Sprintf("%s!A%d:I%d", sheetTransactions, rowNumber, rowNumber)
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, rangeStr).Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return model.Transaction{}, fmt.Errorf("get transaction row: %w", err)
	}
	if len(resp.Values) == 0 {
		return model.Transaction{}, ErrNoTransactions
	}
	return parseTransactionRow(resp.Values[0])
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
