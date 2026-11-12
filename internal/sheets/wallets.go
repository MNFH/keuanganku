package sheets

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
	"google.golang.org/api/sheets/v4"
)

// Wallet balances are spreadsheet formulas, not values the bot writes.
//
// Wallets!B<row> is
//
//	Opening Balance (column D) + income − expense + transfers in − transfers out
//
// summed over the Transactions tab for that wallet, skipping canceled rows.
// The bot only ever appends transactions and marks them canceled; the sheet
// computes every balance, so a balance can never drift from the history,
// and a row edited or canceled by hand in the sheet is reflected at once.
//
// Column D holds the opening balance: whatever a wallet held before its
// first recorded transaction. It is the one balance number a person may edit.

const (
	walletHeaderOpening = "Opening Balance"

	// walletRange reads every wallet column the bot uses.
	walletRange = sheetWallets + "!A2:D"
)

var walletHeaders = []interface{}{"Name", "Balance", "Created At", walletHeaderOpening}

// balanceFormula returns the balance formula for the wallet on sheet row r.
//
// Function names and "," separators are fixed: the Sheets API parses
// formulas in the en_US convention regardless of the spreadsheet's locale.
// SUMIFS is case-insensitive, matching how the bot resolves wallet names. The
// "" criterion on column J matches only empty cells, i.e. active rows.
func balanceFormula(r int) string {
	sum := func(extra string) string {
		return fmt.Sprintf(`SUMIFS(Transactions!$D:$D,Transactions!$E:$E,$A%d,%s,Transactions!$J:$J,"")`, r, extra)
	}
	return fmt.Sprintf("=$D%d+%s-%s+%s-%s", r,
		sum(`Transactions!$C:$C,"income"`),
		sum(`Transactions!$C:$C,"expense"`),
		sum(`Transactions!$C:$C,"transfer",Transactions!$I:$I,"in"`),
		sum(`Transactions!$C:$C,"transfer",Transactions!$I:$I,"out"`),
	)
}

// formulaDelta is a transaction's contribution to balanceFormula, mirrored in
// Go so a migrated wallet's opening balance makes the formula reproduce the
// old balance exactly. It deliberately differs from BalanceDelta for a
// legacy transfer row with no Direction: the formula counts that as nothing,
// so it must here too, and the opening balance absorbs the difference.
func formulaDelta(tx model.Transaction) float64 {
	if tx.Canceled {
		return 0
	}
	switch {
	case strings.EqualFold(string(tx.Type), string(model.Income)):
		return tx.Amount
	case strings.EqualFold(string(tx.Type), string(model.Expense)):
		return -tx.Amount
	case strings.EqualFold(string(tx.Type), string(model.Transfer)):
		switch {
		case strings.EqualFold(string(tx.Direction), string(model.DirectionIn)):
			return tx.Amount
		case strings.EqualFold(string(tx.Direction), string(model.DirectionOut)):
			return -tx.Amount
		}
	}
	return 0
}

// ValidateWalletName rejects names that SUMIFS would read as a pattern or a
// comparison instead of a literal (wildcards * ? ~, or a leading = < >), since
// such a wallet's balance formula would silently sum the wrong rows.
func ValidateWalletName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("nama dompet kosong")
	}
	if strings.ContainsAny(name, "*?~") {
		return fmt.Errorf("nama dompet tidak boleh mengandung * ? ~")
	}
	if strings.ContainsAny(name[:1], "=<>") {
		return fmt.Errorf("nama dompet tidak boleh diawali = < >")
	}
	return nil
}

// needsFormula reports whether a wallet's balance cell (read with the
// FORMULA render option) must be (re)written as the balance formula: it is a
// plain number from before balances were formulas, empty (a wallet just
// appended), or a formula broken by a deleted-and-recreated tab. Any other
// formula is left alone, so a person's deliberate edit is respected.
func needsFormula(cell interface{}) bool {
	s := strings.TrimSpace(fmt.Sprint(cell))
	if cell == nil || s == "" {
		return true
	}
	if strings.HasPrefix(s, "=") {
		return strings.Contains(s, "#REF!")
	}
	return true
}

// ensureWalletFormulas converts every wallet whose balance is not yet a
// formula. A legacy balance is preserved exactly: the opening balance is set
// to (stored balance − what the formula will sum), and the result is read
// back and compared. On any mismatch the old numbers are restored and an error
// returned, so a migration that cannot be trusted changes nothing.
func (c *Client) ensureWalletFormulas(ctx context.Context) error {
	c.formulaMu.Lock()
	defer c.formulaMu.Unlock()

	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, walletRange).
			ValueRenderOption("FORMULA").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return fmt.Errorf("read wallet formulas: %w", err)
	}

	type pending struct {
		row     int
		name    string
		balance float64 // legacy value to preserve
		opening float64 // existing opening balance, for a broken formula
		legacy  bool    // balance cell held a number
	}
	var todo []pending
	for i, row := range resp.Values {
		name := cellString(row, 0)
		if name == "" {
			continue
		}
		bal := cellAt(row, 1)
		if !needsFormula(bal) {
			continue
		}
		p := pending{row: i + 2, name: name}
		if s := strings.TrimSpace(fmt.Sprint(bal)); bal != nil && s != "" && !strings.HasPrefix(s, "=") {
			v, perr := strconv.ParseFloat(s, 64)
			if perr != nil {
				return fmt.Errorf("wallet %q: balance %q is not a number", name, s)
			}
			p.balance, p.legacy = v, true
		} else {
			p.opening, _ = strconv.ParseFloat(cellString(row, 3), 64)
		}
		todo = append(todo, p)
	}
	if len(todo) == 0 {
		return nil
	}

	// What the formula will sum for each wallet, computed the same way.
	txRows, err := c.readTransactionRows(ctx)
	if err != nil {
		return err
	}
	sums := map[string]float64{}
	for _, row := range txRows {
		if tx, perr := parseTransactionRow(row); perr == nil {
			sums[strings.ToLower(tx.Wallet)] += formulaDelta(tx)
		}
	}

	data := []*sheets.ValueRange{{
		Range:  sheetWallets + "!D1",
		Values: [][]interface{}{{walletHeaderOpening}},
	}}
	for i := range todo {
		p := &todo[i]
		if p.legacy {
			p.opening = roundCents(p.balance - sums[strings.ToLower(p.name)])
		}
		data = append(data,
			&sheets.ValueRange{Range: fmt.Sprintf("%s!D%d", sheetWallets, p.row), Values: [][]interface{}{{p.opening}}},
			&sheets.ValueRange{Range: fmt.Sprintf("%s!B%d", sheetWallets, p.row), Values: [][]interface{}{{balanceFormula(p.row)}}},
		)
	}
	if err := c.batchWrite(ctx, data, "USER_ENTERED"); err != nil {
		return fmt.Errorf("write wallet formulas: %w", err)
	}

	// Verify each migrated legacy balance survived unchanged.
	got, err := c.readWalletValues(ctx)
	if err != nil {
		return fmt.Errorf("verify wallet formulas: %w", err)
	}
	var bad []string
	for _, p := range todo {
		if !p.legacy {
			continue
		}
		var row []interface{}
		if idx := p.row - 2; idx < len(got) {
			row = got[idx]
		}
		if v, ok := cellNumber(row, 1); !ok || math.Abs(v-p.balance) > 0.005 {
			bad = append(bad, fmt.Sprintf("%s (was %v, formula gives %v)", p.name, p.balance, cellAt(row, 1)))
		}
	}
	if len(bad) == 0 {
		return nil
	}

	// Restore the old numbers rather than leave balances that changed.
	restore := []*sheets.ValueRange{}
	for _, p := range todo {
		if p.legacy {
			restore = append(restore,
				&sheets.ValueRange{Range: fmt.Sprintf("%s!B%d", sheetWallets, p.row), Values: [][]interface{}{{p.balance}}},
				&sheets.ValueRange{Range: fmt.Sprintf("%s!D%d", sheetWallets, p.row), Values: [][]interface{}{{""}}},
			)
		}
	}
	if rbErr := c.batchWrite(ctx, restore, "RAW"); rbErr != nil {
		return fmt.Errorf("%w: balance formulas gave wrong results (%s) and old balances could not be restored: %v",
			ErrInconsistent, strings.Join(bad, "; "), rbErr)
	}
	return fmt.Errorf("balance formulas gave wrong results, old balances restored: %s", strings.Join(bad, "; "))
}

// readWalletValues reads the wallet rows as computed numbers.
func (c *Client) readWalletValues(ctx context.Context) ([][]interface{}, error) {
	var resp *sheets.ValueRange
	err := c.withRecovery(ctx, func() error {
		var innerErr error
		resp, innerErr = c.svc.Spreadsheets.Values.Get(c.spreadsheetID, walletRange).
			ValueRenderOption("UNFORMATTED_VALUE").Context(ctx).Do()
		return innerErr
	})
	if err != nil {
		return nil, err
	}
	return resp.Values, nil
}

// GetWallets returns every wallet with its computed balance. The first call
// per client also converts any wallet still holding a plain-number balance
// to the formula (see ensureWalletFormulas).
func (c *Client) GetWallets(ctx context.Context) ([]model.Wallet, error) {
	if !c.formulasReady.Load() {
		if err := c.ensureWalletFormulas(ctx); err != nil {
			return nil, fmt.Errorf("prepare wallet formulas: %w", err)
		}
		c.formulasReady.Store(true)
	}

	rows, err := c.readWalletValues(ctx)
	if err != nil {
		return nil, fmt.Errorf("get wallets: %w", err)
	}

	var wallets []model.Wallet
	for _, row := range rows {
		name := cellString(row, 0)
		if name == "" {
			continue
		}
		balance, ok := cellNumber(row, 1)
		if !ok {
			// An error value (#REF!, #NAME?) must not read as a zero balance.
			return nil, fmt.Errorf("wallet %q: balance cell shows %q, check the formula in the Wallets tab", name, fmt.Sprint(cellAt(row, 1)))
		}
		wallets = append(wallets, model.Wallet{Name: name, Balance: balance})
	}
	return wallets, nil
}

// AddWallet appends a wallet with the given opening balance (column D) and
// gives it the balance formula. The name and date are written RAW so a name
// like "123" stays literal text.
func (c *Client) AddWallet(ctx context.Context, name string, opening float64) error {
	if err := ValidateWalletName(name); err != nil {
		return err
	}
	wallets, err := c.GetWallets(ctx)
	if err != nil {
		return err
	}
	for _, w := range wallets {
		if strings.EqualFold(w.Name, name) {
			return fmt.Errorf("dompet %q sudah ada", w.Name)
		}
	}
	if err := c.appendRow(ctx, sheetWallets+"!A:D", []interface{}{name, "", time.Now().Format("2006-01-02 15:04:05"), opening}); err != nil {
		return err
	}
	// The new row has an empty balance cell; this gives it the formula. If it
	// fails, the next GetWallets retries, since an empty cell needs one.
	if err := c.ensureWalletFormulas(ctx); err != nil {
		c.formulasReady.Store(false)
		return fmt.Errorf("dompet ditambahkan tapi rumus saldo gagal dipasang: %w", err)
	}
	return nil
}

// walletExists reports whether a wallet with exactly this stored name exists.
func (c *Client) walletExists(ctx context.Context, name string) error {
	wallets, err := c.GetWallets(ctx)
	if err != nil {
		return err
	}
	for _, w := range wallets {
		if w.Name == name {
			return nil
		}
	}
	return fmt.Errorf("wallet %q not found", name)
}

func (c *Client) batchWrite(ctx context.Context, data []*sheets.ValueRange, inputOption string) error {
	return c.withRecovery(ctx, func() error {
		_, err := c.svc.Spreadsheets.Values.BatchUpdate(c.spreadsheetID, &sheets.BatchUpdateValuesRequest{
			ValueInputOption: inputOption,
			Data:             data,
		}).Context(ctx).Do()
		return err
	})
}

func cellAt(row []interface{}, i int) interface{} {
	if i < len(row) {
		return row[i]
	}
	return nil
}

func cellString(row []interface{}, i int) string {
	v := cellAt(row, i)
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// cellNumber reads an UNFORMATTED_VALUE cell as a number. An empty cell is 0;
// text, including a formula error like "#REF!", is not a number.
func cellNumber(row []interface{}, i int) (float64, bool) {
	switch v := cellAt(row, i).(type) {
	case nil:
		return 0, true
	case float64:
		return v, true
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, true
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func roundCents(v float64) float64 { return math.Round(v*100) / 100 }
