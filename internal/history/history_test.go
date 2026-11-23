package history

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
)

var base = time.Date(2026, 11, 10, 9, 0, 0, 0, time.Local)

func at(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

func tx(id string, typ model.TransactionType, wallet string, t time.Time) model.Transaction {
	return model.Transaction{ID: id, Type: typ, Amount: 1000, Wallet: wallet, Date: t}
}

func leg(id, ref string, dir model.Direction, wallet string, t time.Time) model.Transaction {
	x := tx(id, model.Transfer, wallet, t)
	x.RefID, x.Direction = ref, dir
	return x
}

// show renders a result compactly: ids newest first, a transfer as out>in,
// the undo target suffixed with "!".
func show(r Result) string {
	var s []string
	for _, it := range r.Items {
		id := it.Tx.ID
		if it.In != nil {
			id += ">" + it.In.ID
		}
		if it.Undo {
			id += "!"
		}
		s = append(s, id)
	}
	return fmt.Sprintf("%s (%d)", strings.Join(s, ","), r.Total)
}

func TestSelect(t *testing.T) {
	ledger := []model.Transaction{
		tx("a", model.Expense, "GoPay", at(0)),
		tx("b", model.Income, "BCA", at(10)),
		leg("out", "r1", model.DirectionOut, "BCA", at(20)),
		leg("in", "r1", model.DirectionIn, "GoPay", at(20)),
		tx("c", model.Expense, "BCA", at(30)),
	}
	tests := []struct {
		name   string
		active []model.Transaction
		q      Query
		want   string
	}{
		{"empty ledger", nil, Query{Limit: 10}, " (0)"},
		{"newest first, transfer once, undo on newest", ledger, Query{Limit: 10}, "c!,out>in,b,a (4)"},
		{"limit cuts, total counts all", ledger, Query{Limit: 2}, "c!,out>in (4)"},
		{"fewer than limit is fine", ledger, Query{Limit: 30}, "c!,out>in,b,a (4)"},
		{"wallet filter includes transfers either way", ledger, Query{Wallet: "gopay", Limit: 10}, "out>in,a (2)"},
		{"wallet filter hides the undo target", ledger, Query{Wallet: "GoPay", Limit: 10}, "out>in,a (2)"},
		{"period filter is half-open", ledger,
			Query{HasPeriod: true, From: at(10), To: at(30), Limit: 10}, "out>in,b (2)"},
		{"period and wallet combine", ledger,
			Query{Wallet: "BCA", HasPeriod: true, From: at(0), To: at(25), Limit: 10}, "out>in,b (2)"},
		{"same time: later Ledger row first", []model.Transaction{
			tx("x", model.Expense, "BCA", at(5)),
			tx("y", model.Expense, "BCA", at(5)),
		}, Query{Limit: 10}, "y!,x (2)"},
		{"seconds count before Ledger order", []model.Transaction{
			tx("late", model.Expense, "BCA", at(5).Add(30*time.Second)),
			tx("early", model.Expense, "BCA", at(5)),
		}, Query{Limit: 10}, "late,early! (2)"},
		{"in row before out row still groups", []model.Transaction{
			leg("in", "r1", model.DirectionIn, "GoPay", at(1)),
			leg("out", "r1", model.DirectionOut, "BCA", at(1)),
		}, Query{Limit: 10}, "out>in! (1)"},
		{"lone transfer side (hand-edited sheet)", []model.Transaction{
			leg("in", "r1", model.DirectionIn, "GoPay", at(1)),
		}, Query{Limit: 10}, "in! (1)"},
		{"undo target beyond the limit is not marked", []model.Transaction{
			tx("new", model.Expense, "BCA", at(50)), // hand-edited date
			tx("last", model.Expense, "BCA", at(1)),
		}, Query{Limit: 1}, "new (2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := show(Select(tt.active, tt.q)); got != tt.want {
				t.Errorf("Select = %q, want %q", got, tt.want)
			}
		})
	}
}

// randomLedger builds a ledger of plain rows and transfer pairs, with dates
// that collide and run out of order, as hand edits and shared sheets allow.
func randomLedger(r *rand.Rand) []model.Transaction {
	wallets := []string{"BCA", "GoPay", "Jago Kantong", "007"}
	var out []model.Transaction
	n := r.Intn(25)
	for i := 0; i < n; i++ {
		t := at(r.Intn(8))
		w := wallets[r.Intn(len(wallets))]
		if r.Intn(3) == 0 {
			ref := fmt.Sprintf("r%d", i)
			to := wallets[r.Intn(len(wallets))]
			o := leg(fmt.Sprintf("o%d", i), ref, model.DirectionOut, w, t)
			in := leg(fmt.Sprintf("i%d", i), ref, model.DirectionIn, to, t)
			if r.Intn(2) == 0 {
				out = append(out, o, in)
			} else {
				out = append(out, in, o)
			}
			continue
		}
		typ := model.Expense
		if r.Intn(2) == 0 {
			typ = model.Income
		}
		out = append(out, tx(fmt.Sprintf("t%d", i), typ, w, t))
	}
	return out
}

func randomQuery(r *rand.Rand) Query {
	q := Query{Limit: r.Intn(12)}
	if r.Intn(2) == 0 {
		q.Wallet = []string{"BCA", "gopay", "Jago Kantong", "007"}[r.Intn(4)]
	}
	if r.Intn(2) == 0 {
		q.HasPeriod = true
		q.From = at(r.Intn(6))
		q.To = q.From.Add(time.Duration(1+r.Intn(4)) * time.Minute)
	}
	return q
}

func forAll(t *testing.T, check func(t *testing.T, active []model.Transaction, q Query, res Result)) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		active, q := randomLedger(r), randomQuery(r)
		check(t, active, q, Select(active, q))
		if t.Failed() {
			t.Fatalf("failed on case %d: query %+v, ledger %v", i, q, active)
		}
	}
}

// Property 1: the list is ordered and bounded.
func TestPropertyOrderedAndBounded(t *testing.T) {
	forAll(t, func(t *testing.T, _ []model.Transaction, q Query, res Result) {
		if q.Limit > 0 && len(res.Items) > q.Limit {
			t.Errorf("%d items, limit %d", len(res.Items), q.Limit)
		}
		if res.Total < len(res.Items) {
			t.Errorf("total %d < %d items", res.Total, len(res.Items))
		}
		for i := 1; i < len(res.Items); i++ {
			a, b := res.Items[i-1], res.Items[i]
			if a.Tx.Date.Before(b.Tx.Date) || (a.Tx.Date.Equal(b.Tx.Date) && a.pos < b.pos) {
				t.Errorf("items %d and %d out of order", i-1, i)
			}
		}
	})
}

// Property 2: a transfer appears exactly once, with both sides together.
func TestPropertyTransferOnce(t *testing.T) {
	forAll(t, func(t *testing.T, _ []model.Transaction, _ Query, res Result) {
		seen := map[string]bool{}
		for _, it := range res.Items {
			if it.Tx.RefID == "" {
				continue
			}
			if seen[it.Tx.RefID] {
				t.Errorf("RefID %s in two items", it.Tx.RefID)
			}
			seen[it.Tx.RefID] = true
			if it.In == nil || it.In.RefID != it.Tx.RefID || it.Tx.Direction != model.DirectionOut {
				t.Errorf("transfer %s not grouped as out>in", it.Tx.RefID)
			}
		}
	})
}

// Property 3: filters admit only matches, and with no limit every match.
func TestPropertyFiltersAdmitOnlyMatches(t *testing.T) {
	forAll(t, func(t *testing.T, active []model.Transaction, q Query, res Result) {
		for _, it := range res.Items {
			if q.Wallet != "" && !it.Involves(q.Wallet) {
				t.Errorf("item %s does not involve %s", it.Tx.ID, q.Wallet)
			}
			if q.HasPeriod && (it.Tx.Date.Before(q.From) || !it.Tx.Date.Before(q.To)) {
				t.Errorf("item %s outside the period", it.Tx.ID)
			}
		}
		q.Limit = 0
		all := Select(active, q)
		represented := map[string]bool{}
		for _, it := range all.Items {
			represented[it.Tx.ID] = true
			if it.In != nil {
				represented[it.In.ID] = true
			}
		}
		for _, x := range active {
			if q.Wallet != "" && !strings.EqualFold(x.Wallet, q.Wallet) {
				continue
			}
			if q.HasPeriod && (x.Date.Before(q.From) || !x.Date.Before(q.To)) {
				continue
			}
			if !represented[x.ID] {
				t.Errorf("matching transaction %s missing", x.ID)
			}
		}
	})
}

// Property 4: the undo mark is truthful.
func TestPropertyUndoMarkTruthful(t *testing.T) {
	forAll(t, func(t *testing.T, active []model.Transaction, _ Query, res Result) {
		marked := 0
		for _, it := range res.Items {
			if !it.Undo {
				continue
			}
			marked++
			last := active[len(active)-1]
			if it.Tx.ID != last.ID && (it.In == nil || it.In.ID != last.ID) {
				t.Errorf("item %s marked, but /batal cancels %s", it.Tx.ID, last.ID)
			}
		}
		if marked > 1 {
			t.Errorf("%d items marked", marked)
		}
	})
}

// Unfiltered and unlimited, the target is always shown and marked.
func TestUndoAlwaysMarkedWhenUnfiltered(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 500; i++ {
		active := randomLedger(r)
		res := Select(active, Query{})
		marked := 0
		for _, it := range res.Items {
			if it.Undo {
				marked++
			}
		}
		if len(active) > 0 && marked != 1 {
			t.Fatalf("case %d: %d items marked, want 1", i, marked)
		}
	}
}

func TestFormatEntryDate(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local)
	for in, want := range map[time.Time]string{
		time.Date(2026, 1, 3, 14, 5, 9, 0, time.Local):    "3 Jan 14:05",
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.Local):    "20 Mei 00:00",
		time.Date(2025, 12, 31, 23, 59, 0, 0, time.Local): "31 Des 2025 23:59",
	} {
		if got := FormatEntryDate(in, now); got != want {
			t.Errorf("FormatEntryDate(%v) = %q, want %q", in, got, want)
		}
	}
}
