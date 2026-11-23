package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/nurfaizh/keuanganku/internal/history"
	"github.com/nurfaizh/keuanganku/internal/model"
)

func histTx(id string, typ model.TransactionType, amount float64, wallet, cat, desc string, t time.Time) model.Transaction {
	return model.Transaction{ID: id, Type: typ, Amount: amount, Wallet: wallet, Category: cat, Description: desc, Date: t}
}

func histLeg(id string, dir model.Direction, wallet string, t time.Time) model.Transaction {
	x := histTx(id, model.Transfer, 500000, wallet, "Transfer", "Uang jajan", t)
	x.RefID, x.Direction = "r1", dir
	return x
}

var (
	t1 = time.Date(2026, 11, 3, 14, 5, 0, 0, time.Local)
	t2 = time.Date(2026, 11, 4, 8, 0, 0, 0, time.Local)
	t3 = time.Date(2025, 12, 31, 23, 59, 0, 0, time.Local)
)

func historyLedger() []model.Transaction {
	return []model.Transaction{
		histTx("old", model.Income, 5000000, "BCA", "Gaji", "Gaji\nDesember", t3),
		histTx("e1", model.Expense, 35000, "GoPay", "Makanan", "Makan siang", t1),
		histLeg("out", model.DirectionOut, "BCA", t2),
		histLeg("in", model.DirectionIn, "GoPay", t2),
	}
}

func TestFormatHistoryUnfiltered(t *testing.T) {
	a := historyArgs{Count: 10, Requested: 10}
	got := formatHistory(history.Select(historyLedger(), history.Query{Limit: a.Count}), a, periodNow)
	want := `📜 *Riwayat Transaksi*

🔄 *4 Nov 08:00* · Transfer Rp 500.000
💸 BCA → GoPay
📝 Uang jajan
↩️ _Ini yang dibatalkan oleh /batal_

🔴 *3 Nov 14:05* · Pengeluaran Rp 35.000
💳 GoPay · 🏷️ Makanan
📝 Makan siang

💚 *31 Des 2025 23:59* · Pemasukan Rp 5.000.000
💳 BCA · 🏷️ Gaji
📝 Gaji Desember`
	if got != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestFormatHistoryWalletFilter(t *testing.T) {
	for wallet, line := range map[string]string{
		"GoPay": "⬅️ Masuk ke GoPay dari BCA",
		"BCA":   "➡️ Keluar dari BCA ke GoPay",
	} {
		a := historyArgs{Count: 10, Requested: 10, Wallet: wallet}
		got := formatHistory(history.Select(historyLedger(), history.Query{Wallet: wallet, Limit: 10}), a, periodNow)
		if !strings.HasPrefix(got, "📜 *Riwayat Transaksi* — "+wallet+"\n\n") {
			t.Errorf("%s: header missing wallet:\n%s", wallet, got)
		}
		if !strings.Contains(got, line) {
			t.Errorf("%s: want %q in:\n%s", wallet, line, got)
		}
	}
}

func TestFormatHistoryFooters(t *testing.T) {
	a := historyArgs{Count: 2, Requested: 2}
	got := formatHistory(history.Select(historyLedger(), history.Query{Limit: 2}), a, periodNow)
	if !strings.HasSuffix(got, "\n\n_…dan 1 transaksi lainnya._") {
		t.Errorf("missing 'more' footer:\n%s", got)
	}

	a = historyArgs{Count: maxHistoryCount, Requested: 50}
	got = formatHistory(history.Select(historyLedger(), history.Query{Limit: a.Count}), a, periodNow)
	if !strings.HasSuffix(got, "\n\n_Maksimal 30 transaksi per permintaan._") {
		t.Errorf("missing capped footer:\n%s", got)
	}
}

func TestFormatHistoryPeriodHeader(t *testing.T) {
	p, _, _ := parsePeriod(periodNow, []string{"bulan", "lalu"})
	a := historyArgs{Count: 10, Requested: 10, Period: p, Wallet: "GoPay"}
	got := historyHeader(a)
	if got != "📜 *Riwayat Transaksi* — GoPay — Bulanan Oktober 2026" {
		t.Errorf("header = %q", got)
	}
}

func TestFormatHistoryEmpty(t *testing.T) {
	p, _, _ := parsePeriod(periodNow, []string{"mingguan"})
	tests := []struct {
		a    historyArgs
		want string
	}{
		{historyArgs{}, "📭 Belum ada transaksi.\n" + recordHint},
		{historyArgs{Wallet: "GoPay"}, "📭 Belum ada transaksi untuk *GoPay*."},
		{historyArgs{Period: p}, "📭 Belum ada transaksi pada Mingguan 16–22 Nov 2026."},
		{historyArgs{Wallet: "GoPay", Period: p}, "📭 Belum ada transaksi untuk *GoPay* pada Mingguan 16–22 Nov 2026."},
	}
	for _, tt := range tests {
		if got := formatHistory(history.Result{}, tt.a, periodNow); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
}

// A long description is shown in full, never shortened (R1.5).
func TestFormatHistoryFullDescription(t *testing.T) {
	long := strings.Repeat("panjang sekali ", 400)
	it := history.Item{Tx: histTx("x", model.Expense, 1, "BCA", "Lainnya", long, t1)}
	got := formatHistoryEntry(it, "", periodNow)
	if !strings.Contains(got, strings.TrimSpace(long)) || strings.Contains(got, "\n\n") {
		t.Errorf("description shortened or split into paragraphs")
	}
}
