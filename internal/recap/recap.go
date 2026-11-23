// Package recap computes period-based income/expense statistics and date
// ranges shared by the text /rekap command and the PDF report generator.
package recap

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
)

// CategoryTotal is one row of a category breakdown, sorted by Total descending.
type CategoryTotal struct {
	Category string
	Total    float64
}

// Stats holds the aggregated income/expense figures for a set of transactions.
// model.Transfer transactions are intentionally excluded — moving money
// between your own wallets isn't real income or spending.
type Stats struct {
	TotalIncome       float64
	TotalExpense      float64
	ExpenseByCategory []CategoryTotal
	IncomeByCategory  []CategoryTotal
}

// Compute aggregates a set of transactions into Stats.
func Compute(txs []model.Transaction) Stats {
	incomeByCategory := map[string]float64{}
	expenseByCategory := map[string]float64{}
	var stats Stats

	for _, tx := range txs {
		switch tx.Type {
		case model.Income:
			stats.TotalIncome += tx.Amount
			incomeByCategory[tx.Category] += tx.Amount
		case model.Expense:
			stats.TotalExpense += tx.Amount
			expenseByCategory[tx.Category] += tx.Amount
		}
	}

	stats.ExpenseByCategory = sortedCategoryTotals(expenseByCategory)
	stats.IncomeByCategory = sortedCategoryTotals(incomeByCategory)
	return stats
}

func sortedCategoryTotals(byCategory map[string]float64) []CategoryTotal {
	entries := make([]CategoryTotal, 0, len(byCategory))
	for cat, total := range byCategory {
		entries = append(entries, CategoryTotal{Category: cat, Total: total})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Total > entries[j].Total })
	return entries
}

// DailyTotal is one calendar day's income and expense within a period.
type DailyTotal struct {
	Date    time.Time // local midnight at the start of the day
	Income  float64
	Expense float64
}

// Net returns the day's income minus its expense.
func (d DailyTotal) Net() float64 { return d.Income - d.Expense }

// Daily returns exactly one DailyTotal per calendar day in [from, to), in
// ascending order, including days without any transaction. Like Compute it
// counts only income and expense, never transfers. A transaction is assigned
// to its date's calendar day in from's location; one whose day lies outside
// the period is skipped rather than given a row. from should be a local
// midnight, as every *Range helper returns. Days are stepped with AddDate,
// not 24h, so a DST change can't skip or double a day.
func Daily(txs []model.Transaction, from, to time.Time) []DailyTotal {
	type dayKey struct {
		y int
		m time.Month
		d int
	}
	keyOf := func(t time.Time) dayKey {
		y, m, d := t.In(from.Location()).Date()
		return dayKey{y, m, d}
	}

	var days []DailyTotal
	index := map[dayKey]int{}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		index[keyOf(d)] = len(days)
		days = append(days, DailyTotal{Date: d})
	}

	for _, tx := range txs {
		i, ok := index[keyOf(tx.Date)]
		if !ok {
			continue
		}
		switch tx.Type {
		case model.Income:
			days[i].Income += tx.Amount
		case model.Expense:
			days[i].Expense += tx.Amount
		}
	}
	return days
}

// MonthRange returns [from, to) for the calendar month monthOffset months
// from now (0 = current month, -1 = last month, ...).
func MonthRange(now time.Time, monthOffset int) (from, to time.Time) {
	year, month, _ := now.Date()
	first := time.Date(year, month, 1, 0, 0, 0, 0, now.Location()).AddDate(0, monthOffset, 0)
	return first, first.AddDate(0, 1, 0)
}

// WeekRange returns [from, to) for the Monday-Sunday calendar week
// weekOffset weeks from the week containing now (0 = current week, -1 = last week, ...).
func WeekRange(now time.Time, weekOffset int) (from, to time.Time) {
	weekday := int(now.Weekday())        // Sunday = 0
	daysSinceMonday := (weekday + 6) % 7 // Monday = 0, ..., Sunday = 6
	monday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -daysSinceMonday+7*weekOffset)
	return monday, monday.AddDate(0, 0, 7)
}

// DayRange returns [from, to) for the calendar day dayOffset days from now
// (0 = today, -1 = yesterday, ...).
func DayRange(now time.Time, dayOffset int) (from, to time.Time) {
	from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, dayOffset)
	return from, from.AddDate(0, 0, 1)
}

// FormatDate renders a single date as a human-readable Indonesian date.
func FormatDate(d time.Time) string {
	return fmt.Sprintf("%d %s %d", d.Day(), IndoMonthName(d.Month()), d.Year())
}

// FormatDateRange renders [from, to) as a human-readable Indonesian date range.
func FormatDateRange(from, to time.Time) string {
	lastDay := to.AddDate(0, 0, -1) // to is exclusive
	if from.Month() == lastDay.Month() {
		return fmt.Sprintf("%d–%d %s %d", from.Day(), lastDay.Day(), IndoMonthAbbr(from.Month()), from.Year())
	}
	return fmt.Sprintf("%d %s – %d %s %d", from.Day(), IndoMonthAbbr(from.Month()), lastDay.Day(), IndoMonthAbbr(lastDay.Month()), lastDay.Year())
}

var indoMonthLookup = map[string]time.Month{
	"januari": time.January, "jan": time.January,
	"februari": time.February, "feb": time.February,
	"maret": time.March, "mar": time.March,
	"april": time.April, "apr": time.April,
	"mei": time.May,
	"juni": time.June, "jun": time.June,
	"juli": time.July, "jul": time.July,
	"agustus": time.August, "agu": time.August, "ags": time.August, "aug": time.August,
	"september": time.September, "sep": time.September, "sept": time.September,
	"oktober": time.October, "okt": time.October, "oct": time.October,
	"november": time.November, "nov": time.November,
	"desember": time.December, "des": time.December, "dec": time.December,
}

// ParseIndoMonth parses an Indonesian month name or common abbreviation.
func ParseIndoMonth(s string) (time.Month, bool) {
	m, ok := indoMonthLookup[strings.ToLower(s)]
	return m, ok
}

var indoMonthNames = [...]string{"", "Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
var indoMonthAbbrs = [...]string{"", "Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

func IndoMonthName(m time.Month) string { return indoMonthNames[int(m)] }
func IndoMonthAbbr(m time.Month) string { return indoMonthAbbrs[int(m)] }

var indoWeekdays = [...]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}

// IndoWeekday returns the Indonesian name of a weekday.
func IndoWeekday(d time.Weekday) string { return indoWeekdays[int(d)] }

// FormatDayShort renders a date as day name, day of month and abbreviated
// month, without the year: "Senin, 3 Nov".
func FormatDayShort(d time.Time) string {
	return fmt.Sprintf("%s, %d %s", IndoWeekday(d.Weekday()), d.Day(), IndoMonthAbbr(d.Month()))
}
