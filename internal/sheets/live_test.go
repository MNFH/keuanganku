//go:build live

// Live test against the real Sheets API. It needs a BLANK spreadsheet shared
// with the service account as Editor — never a real ledger, since it adds
// wallets and transactions. (Service accounts have no Drive storage, so the
// test cannot create one itself.) Run deliberately:
//
//	KEUANGANKU_CREDENTIALS=/abs/path/credentials.json \
//	KEUANGANKU_TEST_SPREADSHEET=<blank sheet id> \
//	go test -tags live ./internal/sheets -run Live -v -count=1
package sheets

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

func TestLiveAppendOnlyLedger(t *testing.T) {
	creds := os.Getenv("KEUANGANKU_CREDENTIALS")
	sheetID := os.Getenv("KEUANGANKU_TEST_SPREADSHEET")
	if creds == "" || sheetID == "" {
		t.Skip("KEUANGANKU_CREDENTIALS and KEUANGANKU_TEST_SPREADSHEET must be set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	svc, err := sheets.NewService(ctx, option.WithCredentialsFile(creds), option.WithScopes(sheets.SpreadsheetsScope))
	if err != nil {
		t.Fatal(err)
	}
	ss, err := svc.Spreadsheets.Get(sheetID).Context(ctx).Do()
	if err != nil {
		t.Fatalf("open test spreadsheet: %v", err)
	}
	for _, s := range ss.Sheets {
		if s.Properties.Title == sheetWallets || s.Properties.Title == sheetTransactions {
			t.Fatalf("test spreadsheet already has a %q tab — use a blank sheet, never a real ledger", s.Properties.Title)
		}
	}

	// Remove the tabs this test creates, so the sheet stays blank for reruns.
	t.Cleanup(func() {
		got, err := svc.Spreadsheets.Get(sheetID).Do()
		if err != nil {
			t.Logf("cleanup: %v", err)
			return
		}
		var reqs []*sheets.Request
		for _, s := range got.Sheets {
			if s.Properties.Title == sheetWallets || s.Properties.Title == sheetTransactions {
				reqs = append(reqs, &sheets.Request{DeleteSheet: &sheets.DeleteSheetRequest{SheetId: s.Properties.SheetId}})
			}
		}
		if len(reqs) > 0 {
			if _, err := svc.Spreadsheets.BatchUpdate(sheetID, &sheets.BatchUpdateSpreadsheetRequest{Requests: reqs}).Do(); err != nil {
				t.Logf("cleanup: delete test tabs: %v", err)
			}
		}
	})

	c := &Client{svc: svc, spreadsheetID: ss.SpreadsheetId}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	balances := func() map[string]float64 {
		t.Helper()
		ws, err := c.GetWallets(ctx)
		must(err)
		m := map[string]float64{}
		for _, w := range ws {
			m[w.Name] = w.Balance
		}
		return m
	}
	expect := func(step string, want map[string]float64) {
		t.Helper()
		got := balances()
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %v, want %v (all: %v)", step, k, got[k], v, got)
			}
		}
	}
	now := time.Now()
	newTx := func(id string, typ model.TransactionType, amt float64, wallet string) model.Transaction {
		return model.Transaction{ID: id, Type: typ, Amount: amt, Wallet: wallet, Category: "Tes", Description: "live", Date: now}
	}

	must(c.InitSheets(ctx))

	// A legacy wallet: typed balance, no formula, one transaction already on it.
	_, err = svc.Spreadsheets.Values.Append(ss.SpreadsheetId, "Wallets!A:C", &sheets.ValueRange{
		Values: [][]interface{}{{"legacy", 1000, "2026-01-01 00:00:00"}},
	}).ValueInputOption("RAW").Context(ctx).Do()
	must(err)
	must(c.AddTransactions(ctx, newTx("pre", model.Expense, 100, "legacy")))
	// The typed 1000 already reflects that expense, so after conversion the
	// balance must still read 1000.
	must(c.EnsureBalanceFormulas(ctx))
	expect("legacy converted", map[string]float64{"legacy": 1000})

	must(c.AddWallet(ctx, "jago"))
	// Must stay the text "007", not become the number 7.
	must(c.AddWallet(ctx, "007"))
	// Names SUMIFS would read as a pattern, and a case-insensitive duplicate.
	for _, bad := range []string{"=bsi", "go*pay", ">x", "JAGO"} {
		if err := c.AddWallet(ctx, bad); err == nil {
			t.Errorf("wallet name %q was accepted", bad)
		}
	}
	expect("new wallets", map[string]float64{"jago": 0, "007": 0})

	must(c.AddTransactions(ctx, newTx("i1", model.Income, 500000, "jago")))
	must(c.AddTransactions(ctx, newTx("e1", model.Expense, 25000, "jago")))
	expect("income/expense", map[string]float64{"jago": 475000})

	out := newTx("t-out", model.Transfer, 75000, "jago")
	out.RefID, out.Direction = "ref1", model.DirectionOut
	in := newTx("t-in", model.Transfer, 75000, "007")
	in.RefID, in.Direction = "ref1", model.DirectionIn
	must(c.AddTransactions(ctx, out, in))
	expect("transfer", map[string]float64{"jago": 400000, "007": 75000})

	if err := c.AddTransactions(ctx, newTx("bad", model.Expense, 1, "nope")); err == nil {
		t.Error("unknown wallet was accepted")
	}

	cancelled, err := c.CancelLast(ctx, "batal test")
	must(err)
	if len(cancelled) != 2 {
		t.Fatalf("cancelled %d rows, want both transfer legs", len(cancelled))
	}
	expect("transfer cancelled", map[string]float64{"jago": 475000, "007": 0})

	cancelled, err = c.CancelLast(ctx, "batal test")
	must(err)
	if len(cancelled) != 1 || cancelled[0].ID != "e1" {
		t.Fatalf("second cancel = %+v, want e1", cancelled)
	}
	expect("expense cancelled", map[string]float64{"jago": 500000, "legacy": 1000})

	txs, err := c.GetTransactionsInRange(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	must(err)
	got := map[string]bool{}
	for _, tx := range txs {
		got[tx.ID] = true
	}
	for _, id := range []string{"e1", "t-out", "t-in"} {
		if got[id] {
			t.Errorf("cancelled transaction %s still in recap range", id)
		}
	}
	if !got["i1"] || !got["pre"] {
		t.Errorf("active transactions missing from recap range: %v", got)
	}
}
