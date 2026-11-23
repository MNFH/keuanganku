package recap

import (
	"math/rand"
	"testing"
	"testing/quick"
	"time"
	_ "time/tzdata" // DST zones must load on machines without a zoneinfo database (Windows)

	"github.com/nurfaizh/keuanganku/internal/model"
)

var jakarta = time.FixedZone("WIB", 7*3600)

// randomPeriod picks a local-midnight start in 2020–2029 and a length of
// 1–62 days.
func randomPeriod(r *rand.Rand, loc *time.Location) (from, to time.Time, n int) {
	from = time.Date(2020+r.Intn(10), time.Month(1+r.Intn(12)), 1+r.Intn(28), 0, 0, 0, 0, loc)
	n = 1 + r.Intn(62)
	return from, from.AddDate(0, 0, n), n
}

// randomTxs returns up to 40 transactions of every type, with integer-rupiah
// amounts, dated at a random wall-clock time on a random day of the period.
func randomTxs(r *rand.Rand, from time.Time, n int) []model.Transaction {
	types := []model.TransactionType{model.Income, model.Expense, model.Transfer}
	txs := make([]model.Transaction, r.Intn(41))
	for i := range txs {
		day := from.AddDate(0, 0, r.Intn(n))
		txs[i] = model.Transaction{
			Type:   types[r.Intn(len(types))],
			Amount: float64(1 + r.Intn(5_000_000)),
			Date:   time.Date(day.Year(), day.Month(), day.Day(), r.Intn(24), r.Intn(60), r.Intn(60), 0, day.Location()),
		}
	}
	return txs
}

func quickCheck(t *testing.T, prop func(seed int64) bool) {
	t.Helper()
	if err := quick.Check(prop, &quick.Config{MaxCount: 500}); err != nil {
		t.Error(err)
	}
}

// Property 1: one row per day, in order, all inside the period.
func TestDailyOneRowPerDayInOrder(t *testing.T) {
	quickCheck(t, func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		from, to, n := randomPeriod(r, jakarta)
		days := Daily(randomTxs(r, from, n), from, to)
		if len(days) != n {
			return false
		}
		for i, d := range days {
			if !d.Date.Equal(from.AddDate(0, 0, i)) || d.Date.Before(from) || !d.Date.Before(to) {
				return false
			}
		}
		return true
	})
}

// Property 2: daily totals equal the period totals; transfers count on neither side.
func TestDailyTotalsMatchCompute(t *testing.T) {
	quickCheck(t, func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		from, to, n := randomPeriod(r, jakarta)
		txs := randomTxs(r, from, n)
		var income, expense float64
		for _, d := range Daily(txs, from, to) {
			income += d.Income
			expense += d.Expense
		}
		stats := Compute(txs)
		return income == stats.TotalIncome && expense == stats.TotalExpense
	})
}

// Property 3: no transactions gives every day zero.
func TestDailyEmptyInputAllZero(t *testing.T) {
	quickCheck(t, func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		from, to, n := randomPeriod(r, jakarta)
		days := Daily(nil, from, to)
		if len(days) != n {
			return false
		}
		for _, d := range days {
			if d.Income != 0 || d.Expense != 0 || d.Net() != 0 {
				return false
			}
		}
		return true
	})
}

// Property 4: a transaction lands only on its own local day, at either edge of it.
func TestDailyTransactionLandsOnItsDay(t *testing.T) {
	quickCheck(t, func(seed int64) bool {
		r := rand.New(rand.NewSource(seed))
		from, to, n := randomPeriod(r, jakarta)
		i := r.Intn(n)
		day := from.AddDate(0, 0, i)
		times := []time.Time{
			day,
			time.Date(day.Year(), day.Month(), day.Day(), 23, 59, 59, 0, jakarta),
			time.Date(day.Year(), day.Month(), day.Day(), r.Intn(24), r.Intn(60), r.Intn(60), 0, jakarta),
		}
		for _, at := range times {
			typ := model.Income
			if r.Intn(2) == 0 {
				typ = model.Expense
			}
			days := Daily([]model.Transaction{{Type: typ, Amount: 1000, Date: at}}, from, to)
			for j, d := range days {
				if (j == i) != (d.Income+d.Expense != 0) {
					return false
				}
			}
		}
		return true
	})
}

func TestDailyPeriodLengths(t *testing.T) {
	ref := time.Date(2025, 3, 12, 15, 0, 0, 0, jakarta) // a Wednesday
	tests := []struct {
		name     string
		from, to time.Time
		want     int
		first    time.Weekday
	}{
		{"february non-leap", time.Date(2025, 2, 1, 0, 0, 0, 0, jakarta), time.Date(2025, 3, 1, 0, 0, 0, 0, jakarta), 28, time.Saturday},
		{"february leap", time.Date(2024, 2, 1, 0, 0, 0, 0, jakarta), time.Date(2024, 3, 1, 0, 0, 0, 0, jakarta), 29, time.Thursday},
		{"30-day month", time.Date(2025, 4, 1, 0, 0, 0, 0, jakarta), time.Date(2025, 5, 1, 0, 0, 0, 0, jakarta), 30, time.Tuesday},
		{"31-day month", time.Date(2025, 1, 1, 0, 0, 0, 0, jakarta), time.Date(2025, 2, 1, 0, 0, 0, 0, jakarta), 31, time.Wednesday},
		{"single day", time.Date(2025, 1, 1, 0, 0, 0, 0, jakarta), time.Date(2025, 1, 2, 0, 0, 0, 0, jakarta), 1, time.Wednesday},
	}
	weekFrom, weekTo := WeekRange(ref, 0)
	tests = append(tests, struct {
		name     string
		from, to time.Time
		want     int
		first    time.Weekday
	}{"monday-sunday week", weekFrom, weekTo, 7, time.Monday})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := Daily(nil, tt.from, tt.to)
			if len(days) != tt.want {
				t.Fatalf("len = %d, want %d", len(days), tt.want)
			}
			if got := days[0].Date.Weekday(); got != tt.first {
				t.Errorf("first day = %v, want %v", got, tt.first)
			}
		})
	}
}

func TestDailyAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// March 2025 springs forward on the 9th, so that day is only 23 hours long.
	from, to := MonthRange(time.Date(2025, 3, 15, 12, 0, 0, 0, ny), 0)
	late := time.Date(2025, 3, 9, 23, 30, 0, 0, ny)
	days := Daily([]model.Transaction{{Type: model.Expense, Amount: 500, Date: late}}, from, to)
	if len(days) != 31 {
		t.Fatalf("len = %d, want 31", len(days))
	}
	for i, d := range days {
		if d.Date.Day() != i+1 || d.Date.Hour() != 0 {
			t.Errorf("day %d: Date = %v, want local midnight of March %d", i, d.Date, i+1)
		}
	}
	if days[8].Expense != 500 || days[9].Expense != 0 {
		t.Errorf("expense on 9th/10th = %v/%v, want 500/0", days[8].Expense, days[9].Expense)
	}
}

func TestDailySkipsTransfersAndOutOfRange(t *testing.T) {
	from := time.Date(2025, 5, 5, 0, 0, 0, 0, jakarta)
	to := from.AddDate(0, 0, 7)
	txs := []model.Transaction{
		{Type: model.Transfer, Direction: model.DirectionOut, Amount: 100_000, Date: from.Add(time.Hour)},
		{Type: model.Transfer, Direction: model.DirectionIn, Amount: 100_000, Date: from.Add(time.Hour)},
		{Type: model.Expense, Amount: 20_000, Date: from.Add(-time.Second)}, // the day before
		{Type: model.Income, Amount: 30_000, Date: to},                      // the day after (to is exclusive)
		{Type: model.Income, Amount: 75_000, Date: from.AddDate(0, 0, 2).Add(9 * time.Hour)},
		{Type: model.Expense, Amount: 25_000, Date: from.AddDate(0, 0, 2).Add(18 * time.Hour)},
	}
	days := Daily(txs, from, to)
	if len(days) != 7 {
		t.Fatalf("len = %d, want 7", len(days))
	}
	for i, d := range days {
		wantIn, wantOut := 0.0, 0.0
		if i == 2 {
			wantIn, wantOut = 75_000, 25_000
		}
		if d.Income != wantIn || d.Expense != wantOut {
			t.Errorf("day %d = %v/%v, want %v/%v", i, d.Income, d.Expense, wantIn, wantOut)
		}
	}
	if net := days[2].Net(); net != 50_000 {
		t.Errorf("Net = %v, want 50000", net)
	}
}

func TestDailyEmptyPeriod(t *testing.T) {
	from := time.Date(2025, 5, 5, 0, 0, 0, 0, jakarta)
	if got := Daily(nil, from, from); len(got) != 0 {
		t.Errorf("from == to: len = %d, want 0", len(got))
	}
	if got := Daily(nil, from, from.AddDate(0, 0, -3)); len(got) != 0 {
		t.Errorf("from > to: len = %d, want 0", len(got))
	}
}
