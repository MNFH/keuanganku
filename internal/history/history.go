// Package history chooses and orders the entries shown by /riwayat.
//
// It is pure logic over the active transactions of a ledger, so every rule —
// how a transfer's two rows become one entry, how filters combine, which entry
// /batal would cancel — is testable without Google Sheets. Rendering the
// entries as Indonesian text is the handler's job.
package history

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
	"github.com/nurfaizh/keuanganku/internal/recap"
)

// Query selects which entries to show.
type Query struct {
	Wallet    string    // canonical wallet name; "" means every wallet
	From, To  time.Time // the period [From, To); used only when HasPeriod
	HasPeriod bool
	Limit     int // maximum number of Items; <= 0 means no limit
}

// Item is one entry in the list: a single income or expense, or one transfer.
type Item struct {
	// Tx is the transaction itself, or for a transfer its out side (the in
	// side if the out side is missing, which only a hand-edited sheet can
	// cause).
	Tx model.Transaction
	// In is the in side of a transfer whose out side is Tx; nil otherwise.
	In *model.Transaction
	// Undo marks the entry /batal would cancel.
	Undo bool

	pos int // Ledger position of the later row, for ordering
}

// Result is the selected entries plus how many matched before the limit.
type Result struct {
	Items []Item
	Total int
}

// Involves reports whether the entry touches wallet (case-insensitive).
func (it Item) Involves(wallet string) bool {
	if strings.EqualFold(it.Tx.Wallet, wallet) {
		return true
	}
	return it.In != nil && strings.EqualFold(it.In.Wallet, wallet)
}

// Select returns the entries matching q, newest first.
//
// active must be the ledger's active transactions in Ledger order (the order
// rows were appended), as returned by sheets.Client.GetActiveTransactions.
// Entries are ordered by recorded time to the second, newest first; entries
// recorded at the same time are ordered by Ledger position, later first.
func Select(active []model.Transaction, q Query) Result {
	items := group(active)

	matched := items[:0]
	for _, it := range items {
		if q.Wallet != "" && !it.Involves(q.Wallet) {
			continue
		}
		if q.HasPeriod && (it.Tx.Date.Before(q.From) || !it.Tx.Date.Before(q.To)) {
			continue
		}
		matched = append(matched, it)
	}

	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		if !a.Tx.Date.Equal(b.Tx.Date) {
			return a.Tx.Date.After(b.Tx.Date)
		}
		return a.pos > b.pos
	})

	res := Result{Total: len(matched), Items: matched}
	if q.Limit > 0 && len(res.Items) > q.Limit {
		res.Items = res.Items[:q.Limit]
	}
	markUndo(active, res.Items)
	return res
}

// group turns rows into entries, merging the two sides of each transfer.
func group(active []model.Transaction) []Item {
	var items []Item
	byRef := map[string]int{} // RefID -> index in items
	for i, tx := range active {
		if tx.Type != model.Transfer || tx.RefID == "" {
			items = append(items, Item{Tx: tx, pos: i})
			continue
		}
		k, seen := byRef[tx.RefID]
		if !seen {
			byRef[tx.RefID] = len(items)
			items = append(items, Item{Tx: tx, pos: i})
			continue
		}
		it := &items[k]
		it.pos = i
		other := tx
		if tx.Direction == model.DirectionOut {
			// Keep the out side in Tx, whichever order the rows landed in.
			other, it.Tx = it.Tx, tx
		}
		it.In = &other
	}
	return items
}

// markUndo sets Undo on the shown entry holding the last active transaction,
// which is exactly what /batal cancels. When that entry was filtered out or
// cut by the limit, nothing is marked.
func markUndo(active []model.Transaction, shown []Item) {
	if len(active) == 0 {
		return
	}
	last := len(active) - 1
	target := active[last]
	for i := range shown {
		it := &shown[i]
		if target.Type == model.Transfer && target.RefID != "" {
			if it.Tx.RefID == target.RefID {
				it.Undo = true
				return
			}
			continue
		}
		if it.pos == last && it.In == nil && it.Tx.ID == target.ID {
			it.Undo = true
			return
		}
	}
}

// FormatEntryDate renders a recorded time as "3 Jan 14:05", adding the year
// ("3 Jan 2025 14:05") when it differs from now's year in the bot's local
// time.
func FormatEntryDate(t, now time.Time) string {
	t = t.In(now.Location())
	if t.Year() != now.Year() {
		return fmt.Sprintf("%d %s %d %s", t.Day(), recap.IndoMonthAbbr(t.Month()), t.Year(), t.Format("15:04"))
	}
	return fmt.Sprintf("%d %s %s", t.Day(), recap.IndoMonthAbbr(t.Month()), t.Format("15:04"))
}
