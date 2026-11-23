package report

import (
	"bytes"
	"testing"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
)

var wib = time.FixedZone("WIB", 7*3600)

func TestGenerateMonthlyProducesPDF(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, wib)
	for _, n := range []int{1, 7, 31} {
		out, err := GenerateMonthly("Rekap", from, from.AddDate(0, 0, n), nil, nil)
		if err != nil {
			t.Fatalf("%d days: %v", n, err)
		}
		if !bytes.HasPrefix(out, []byte("%PDF")) {
			t.Errorf("%d days: output does not start with %%PDF", n)
		}
	}
}

// With no transactions the earlier sections fit on one page, so the extra
// page of a month can only come from the daily table, and a single day gets
// none.
func TestDailyTableOnlyForMultiDayPeriods(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, wib)
	single := buildMonthly("Rekap", from, from.AddDate(0, 0, 1), nil, nil)
	month := buildMonthly("Rekap", from, from.AddDate(0, 0, 31), nil, nil)
	if err := single.Error(); err != nil {
		t.Fatal(err)
	}
	if err := month.Error(); err != nil {
		t.Fatal(err)
	}
	if got := single.PageCount(); got != 1 {
		t.Errorf("single day: %d pages, want 1", got)
	}
	if month.PageCount() <= single.PageCount() {
		t.Errorf("31 days: %d pages, want more than the single day's %d", month.PageCount(), single.PageCount())
	}
}

// Every section, the daily table included, must survive a busy month: all
// categories, transfers, many wallets and a long table split across pages.
func TestGenerateMonthlyBusyMonth(t *testing.T) {
	from := time.Date(2025, 3, 1, 0, 0, 0, 0, wib)
	var txs []model.Transaction
	for d := 0; d < 31; d++ {
		at := from.AddDate(0, 0, d).Add(10 * time.Hour)
		txs = append(txs,
			model.Transaction{Type: model.Expense, Amount: float64(10_000 * (d + 1)), Category: "Makanan", Date: at},
			model.Transaction{Type: model.Transfer, Direction: model.DirectionOut, Amount: 5_000, Date: at},
		)
		if d%7 == 0 {
			txs = append(txs, model.Transaction{Type: model.Income, Amount: 1_000_000, Category: "Gaji", Date: at})
		}
	}
	wallets := []model.Wallet{{Name: "BCA", Balance: 2_500_000}, {Name: "GoPay", Balance: -15_000}}
	pdf := buildMonthly("Rekap Bulanan — Maret 2025", from, from.AddDate(0, 1, 0), txs, wallets)
	if err := pdf.Error(); err != nil {
		t.Fatal(err)
	}
	if pdf.PageCount() < 2 {
		t.Errorf("%d pages, want the daily table to spill onto a second page", pdf.PageCount())
	}
}
