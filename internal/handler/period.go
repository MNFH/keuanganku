package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/recap"
)

// period is a [From, To) window named by the user, shared by /rekap and
// /riwayat so both accept exactly the same words and boundaries.
type period struct {
	From, To time.Time
	Kind     string // "Harian", "Mingguan" or "Bulanan"
	Label    string // e.g. "Januari 2025", "3–9 Nov 2026", "15 Januari 2026"
}

// parsePeriod reads a period from the front of tokens and reports how many
// tokens it used, so /riwayat can read a wallet name after it.
//
// Grammar (keywords case-insensitive, English aliases accepted):
//
//	bulan|bulanan   [ini | lalu | BULAN [TAHUN]]
//	minggu|mingguan [ini | lalu | TGL [BULAN] [TAHUN]]
//	hari|harian     [ini | lalu | TGL [BULAN] [TAHUN]]
//	kemarin
//
// It returns (nil, 0, "") when tokens do not start with a period, and an
// Indonesian errMsg when they are shaped like a date that does not exist.
// Optional parts are consumed only when they fit, so a following wallet name
// is never mistaken for a month or a year.
func parsePeriod(now time.Time, tokens []string) (p *period, used int, errMsg string) {
	if len(tokens) == 0 {
		return nil, 0, ""
	}
	rest := tokens[1:]
	switch strings.ToLower(tokens[0]) {
	case "bulan", "bulanan", "month", "monthly":
		p, n, errMsg := monthPeriod(now, rest)
		return p, 1 + n, errMsg
	case "minggu", "mingguan", "week", "weekly":
		p, n, errMsg := weekPeriod(now, rest)
		return p, 1 + n, errMsg
	case "harian", "hari", "today", "day", "daily":
		p, n, errMsg := dayPeriod(now, rest)
		return p, 1 + n, errMsg
	case "kemarin", "yesterday":
		from, to := recap.DayRange(now, -1)
		return dayOf(from, to), 1, ""
	}

	// Bare forms, without a keyword: "5 januari [2026]" is that day and
	// "januari [2026]" that month. A number not followed by a month name is
	// not a period, so /riwayat reads it as the count instead.
	if isDigits(tokens[0]) {
		if len(tokens) < 2 {
			return nil, 0, ""
		}
		if _, ok := recap.ParseIndoMonth(tokens[1]); !ok {
			return nil, 0, ""
		}
		ref, n, errMsg := dateArgs(now, tokens, "harian")
		if errMsg != "" {
			return nil, 0, errMsg
		}
		from, to := recap.DayRange(ref, 0)
		return dayOf(from, to), n, ""
	}
	if month, ok := recap.ParseIndoMonth(tokens[0]); ok {
		year, n, errMsg := optionalYear(now, rest)
		if errMsg != "" {
			return nil, 0, errMsg
		}
		from := time.Date(year, month, 1, 0, 0, 0, 0, now.Location())
		return monthOf(from, from.AddDate(0, 1, 0)), 1 + n, ""
	}
	return nil, 0, ""
}

func monthPeriod(now time.Time, rest []string) (*period, int, string) {
	if len(rest) > 0 {
		switch {
		case isPrevPeriodKeyword(rest[0]):
			from, to := recap.MonthRange(now, -1)
			return monthOf(from, to), 1, ""
		case isTodayKeyword(rest[0]):
			from, to := recap.MonthRange(now, 0)
			return monthOf(from, to), 1, ""
		}
		if month, ok := recap.ParseIndoMonth(rest[0]); ok {
			year, n, errMsg := optionalYear(now, rest[1:])
			if errMsg != "" {
				return nil, 0, errMsg
			}
			from := time.Date(year, month, 1, 0, 0, 0, 0, now.Location())
			return monthOf(from, from.AddDate(0, 1, 0)), 1 + n, ""
		}
	}
	from, to := recap.MonthRange(now, 0)
	return monthOf(from, to), 0, ""
}

func weekPeriod(now time.Time, rest []string) (*period, int, string) {
	if len(rest) > 0 {
		switch {
		case isPrevPeriodKeyword(rest[0]):
			from, to := recap.WeekRange(now, -1)
			return weekOf(from, to), 1, ""
		case isTodayKeyword(rest[0]):
			from, to := recap.WeekRange(now, 0)
			return weekOf(from, to), 1, ""
		}
		if isDigits(rest[0]) {
			ref, n, errMsg := dateArgs(now, rest, "minggu")
			if errMsg != "" {
				return nil, 0, errMsg
			}
			from, to := recap.WeekRange(ref, 0)
			return weekOf(from, to), n, ""
		}
	}
	from, to := recap.WeekRange(now, 0)
	return weekOf(from, to), 0, ""
}

func dayPeriod(now time.Time, rest []string) (*period, int, string) {
	if len(rest) > 0 {
		switch {
		case isPrevPeriodKeyword(rest[0]):
			from, to := recap.DayRange(now, -1)
			return dayOf(from, to), 1, ""
		case isTodayKeyword(rest[0]):
			from, to := recap.DayRange(now, 0)
			return dayOf(from, to), 1, ""
		}
		if isDigits(rest[0]) {
			ref, n, errMsg := dateArgs(now, rest, "harian")
			if errMsg != "" {
				return nil, 0, errMsg
			}
			from, to := recap.DayRange(ref, 0)
			return dayOf(from, to), n, ""
		}
	}
	from, to := recap.DayRange(now, 0)
	return dayOf(from, to), 0, ""
}

// dateArgs reads TGL [BULAN] [TAHUN]; tokens[0] is known to be digits. The
// month defaults to now's and the year to now's. The day must exist in that
// month: time.Date would silently turn 31 Februari into 3 Maret.
func dateArgs(now time.Time, tokens []string, keyword string) (time.Time, int, string) {
	day, err := strconv.Atoi(tokens[0])
	if err != nil || day < 1 || day > 31 {
		return time.Time{}, 0, fmt.Sprintf("❌ Tanggal tidak valid: *%s*\nContoh: %s 15 januari 2026", tokens[0], keyword)
	}
	used := 1
	month := now.Month()
	year := now.Year()
	if len(tokens) > 1 {
		if m, ok := recap.ParseIndoMonth(tokens[1]); ok {
			month = m
			used++
			y, n, errMsg := optionalYear(now, tokens[2:])
			if errMsg != "" {
				return time.Time{}, 0, errMsg
			}
			year, used = y, used+n
		}
	}
	if day > daysIn(year, month) {
		return time.Time{}, 0, fmt.Sprintf("❌ Tanggal tidak valid: *%d %s %d*", day, recap.IndoMonthName(month), year)
	}
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location()), used, ""
}

// optionalYear reads a year from tokens[0] when it is all digits; anything
// else is left for the caller (it may be a wallet name).
func optionalYear(now time.Time, tokens []string) (year, used int, errMsg string) {
	if len(tokens) == 0 || !isDigits(tokens[0]) {
		return now.Year(), 0, ""
	}
	y, err := strconv.Atoi(tokens[0])
	if err != nil || y < 2000 || y > 2100 {
		return 0, 0, fmt.Sprintf("❌ Tahun tidak valid: *%s*", tokens[0])
	}
	return y, 1, ""
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func monthOf(from, to time.Time) *period {
	return &period{From: from, To: to, Kind: "Bulanan", Label: fmt.Sprintf("%s %d", recap.IndoMonthName(from.Month()), from.Year())}
}

func weekOf(from, to time.Time) *period {
	return &period{From: from, To: to, Kind: "Mingguan", Label: recap.FormatDateRange(from, to)}
}

func dayOf(from, to time.Time) *period {
	return &period{From: from, To: to, Kind: "Harian", Label: recap.FormatDate(from)}
}

func isPrevPeriodKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "lalu", "kemarin", "sebelumnya":
		return true
	}
	return false
}

func isTodayKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "ini", "sekarang":
		return true
	}
	return false
}
