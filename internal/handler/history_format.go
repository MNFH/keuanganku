package handler

import (
	"fmt"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/history"
	"github.com/nurfaizh/keuanganku/internal/model"
)

// recordHint is shown on an empty, unfiltered history: the ledger has nothing
// yet, so say how to start.
const recordHint = "Catat dengan: /keluar 35rb GoPay Makanan Makan siang"

// formatHistory renders /riwayat's reply. Each entry is one paragraph with no
// blank line inside it, and paragraphs are separated by a blank line, so
// messaging.SplitText can split a long list without cutting an entry.
func formatHistory(res history.Result, a historyArgs, now time.Time) string {
	if res.Total == 0 {
		return formatHistoryEmpty(a)
	}

	paras := []string{historyHeader(a)}
	for _, it := range res.Items {
		paras = append(paras, formatHistoryEntry(it, a.Wallet, now))
	}

	var footer []string
	if more := res.Total - len(res.Items); more > 0 {
		footer = append(footer, fmt.Sprintf("_…dan %d transaksi lainnya._", more))
	}
	if a.Requested > maxHistoryCount {
		footer = append(footer, fmt.Sprintf("_Maksimal %d transaksi per permintaan._", maxHistoryCount))
	}
	if len(footer) > 0 {
		paras = append(paras, strings.Join(footer, "\n"))
	}
	return strings.Join(paras, "\n\n")
}

func historyHeader(a historyArgs) string {
	h := "📜 *Riwayat Transaksi*"
	if a.Wallet != "" {
		h += " — " + a.Wallet
	}
	if a.Period != nil {
		h += " — " + a.Period.Kind + " " + a.Period.Label
	}
	return h
}

// formatHistoryEmpty is the single reply for "nothing matched", naming
// whichever filters were given.
func formatHistoryEmpty(a historyArgs) string {
	msg := "📭 Belum ada transaksi"
	if a.Wallet != "" {
		msg += " untuk *" + a.Wallet + "*"
	}
	if a.Period != nil {
		msg += " pada " + a.Period.Kind + " " + a.Period.Label
	}
	msg += "."
	if a.Wallet == "" && a.Period == nil {
		msg += "\n" + recordHint
	}
	return msg
}

// formatHistoryEntry renders one entry. wallet is the active wallet filter,
// used to say which way a transfer moved money for that wallet.
func formatHistoryEntry(it history.Item, wallet string, now time.Time) string {
	tx := it.Tx
	date := history.FormatEntryDate(tx.Date, now)
	var lines []string

	switch {
	case tx.Type == model.Transfer:
		lines = append(lines, fmt.Sprintf("🔄 *%s* · Transfer Rp %s", date, model.FormatAmount(tx.Amount)))
		lines = append(lines, transferLine(it, wallet))
	default:
		label := "💚 *%s* · Pemasukan Rp %s"
		if tx.Type == model.Expense {
			label = "🔴 *%s* · Pengeluaran Rp %s"
		}
		lines = append(lines, fmt.Sprintf(label, date, model.FormatAmount(tx.Amount)))
		detail := "💳 " + tx.Wallet
		if c := oneLine(tx.Category); c != "" {
			detail += " · 🏷️ " + c
		}
		lines = append(lines, detail)
	}

	if d := oneLine(tx.Description); d != "" {
		lines = append(lines, "📝 "+d)
	}
	if it.Undo {
		lines = append(lines, "↩️ _Ini yang dibatalkan oleh /batal_")
	}
	return strings.Join(lines, "\n")
}

// transferLine names both wallets. Under a wallet filter it says whether
// money went into or out of that wallet.
func transferLine(it history.Item, wallet string) string {
	if it.In == nil {
		// A lone side, which only a hand-edited sheet produces.
		if it.Tx.Direction == model.DirectionIn {
			return "⬅️ Masuk ke " + it.Tx.Wallet
		}
		return "➡️ Keluar dari " + it.Tx.Wallet
	}
	from, to := it.Tx.Wallet, it.In.Wallet
	switch {
	case wallet != "" && strings.EqualFold(wallet, to) && !strings.EqualFold(wallet, from):
		return fmt.Sprintf("⬅️ Masuk ke %s dari %s", to, from)
	case wallet != "" && strings.EqualFold(wallet, from) && !strings.EqualFold(wallet, to):
		return fmt.Sprintf("➡️ Keluar dari %s ke %s", from, to)
	}
	return fmt.Sprintf("💸 %s → %s", from, to)
}

// oneLine keeps the whole text but folds line breaks and runs of spaces, so
// a hand-edited multi-line description cannot open a blank line inside an
// entry.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
