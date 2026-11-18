package handler

import (
	"strings"
	"testing"
	"time"
)

var periodNow = time.Date(2026, 11, 18, 10, 30, 0, 0, time.Local)

// TestResolveRekapUnchanged pins /rekap's titles and ranges. The expected
// values were captured from the resolvers this parser replaced, so the
// extraction cannot have changed what users see.
func TestResolveRekapUnchanged(t *testing.T) {
	tests := []struct{ in, title, from, to string }{
		{"", "Rekap Bulanan — November 2026", "2026-11-01", "2026-12-01"},
		{"bulan", "Rekap Bulanan — November 2026", "2026-11-01", "2026-12-01"},
		{"bulanan lalu", "Rekap Bulanan — Oktober 2026", "2026-10-01", "2026-11-01"},
		{"bulan januari 2025", "Rekap Bulanan — Januari 2025", "2025-01-01", "2025-02-01"},
		{"bulan januari", "Rekap Bulanan — Januari 2026", "2026-01-01", "2026-02-01"},
		{"month mar", "Rekap Bulanan — Maret 2026", "2026-03-01", "2026-04-01"},
		{"minggu", "Rekap Mingguan — 16–22 Nov 2026", "2026-11-16", "2026-11-23"},
		{"mingguan lalu", "Rekap Mingguan — 9–15 Nov 2026", "2026-11-09", "2026-11-16"},
		{"minggu 3 januari 2026", "Rekap Mingguan — 29 Des – 4 Jan 2026", "2025-12-29", "2026-01-05"},
		{"minggu 3", "Rekap Mingguan — 2–8 Nov 2026", "2026-11-02", "2026-11-09"},
		{"harian", "Rekap Harian — 18 November 2026", "2026-11-18", "2026-11-19"},
		{"hari ini", "Rekap Harian — 18 November 2026", "2026-11-18", "2026-11-19"},
		{"harian lalu", "Rekap Harian — 17 November 2026", "2026-11-17", "2026-11-18"},
		{"kemarin", "Rekap Harian — 17 November 2026", "2026-11-17", "2026-11-18"},
		{"harian 15 januari 2026", "Rekap Harian — 15 Januari 2026", "2026-01-15", "2026-01-16"},
		{"harian 15", "Rekap Harian — 15 November 2026", "2026-11-15", "2026-11-16"},
		{"yesterday", "Rekap Harian — 17 November 2026", "2026-11-17", "2026-11-18"},
		{"daily sekarang", "Rekap Harian — 18 November 2026", "2026-11-18", "2026-11-19"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p, errMsg := resolveRekap(periodNow, strings.Fields(tt.in))
			if errMsg != "" {
				t.Fatalf("error: %s", errMsg)
			}
			title := "Rekap " + p.Kind + " — " + p.Label
			if title != tt.title || p.From.Format("2006-01-02") != tt.from || p.To.Format("2006-01-02") != tt.to {
				t.Errorf("got %q %s..%s, want %q %s..%s", title,
					p.From.Format("2006-01-02"), p.To.Format("2006-01-02"), tt.title, tt.from, tt.to)
			}
		})
	}
}

func TestResolveRekapErrors(t *testing.T) {
	tests := []struct{ in, want string }{
		{"harian 31 februari", "Tanggal tidak valid: *31 Februari 2026*"},
		{"harian 29 februari 2025", "Tanggal tidak valid: *29 Februari 2025*"},
		{"minggu 32 januari", "Tanggal tidak valid: *32*"},
		{"bulan januari 1999", "Tahun tidak valid: *1999*"},
		{"bulan janauri", "Tidak dikenali: *janauri*"},
		{"mingguan xyz", "Tidak dikenali: *xyz*"},
		{"tahunan", "❌ Format:"},
		// Bare dates that do not exist (R8.3).
		{"31 feb", "Tanggal tidak valid: *31 Februari 2026*"},
		{"32 januari", "Tanggal tidak valid: *32*"},
		{"5 januari 1999", "Tahun tidak valid: *1999*"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p, errMsg := resolveRekap(periodNow, strings.Fields(tt.in))
			if p != nil || !strings.Contains(errMsg, tt.want) {
				t.Errorf("got period %v, error %q; want error containing %q", p, errMsg, tt.want)
			}
		})
	}
}

// TestParsePeriodUsed checks that optional parts are taken only when they
// fit, leaving the rest (a wallet name, for /riwayat) unconsumed.
func TestParsePeriodUsed(t *testing.T) {
	tests := []struct {
		in    string
		used  int
		label string // "" means no period
	}{
		{"GoPay", 0, ""},
		{"mingguan GoPay", 1, "16–22 Nov 2026"},
		{"bulan ini Jago Kantong", 2, "November 2026"},
		{"bulan januari GoPay", 2, "Januari 2026"},
		{"bulan januari 2025 GoPay", 3, "Januari 2025"},
		{"harian 15 GoPay", 2, "15 November 2026"},
		{"harian 15 januari BCA", 3, "15 Januari 2026"},
		{"kemarin BCA", 1, "17 November 2026"},
		{"minggu ini", 2, "16–22 Nov 2026"},
		// Bare forms (R8.4, R8.5).
		{"5 januari", 2, "5 Januari 2026"},
		{"5 januari 2025 GoPay", 3, "5 Januari 2025"},
		{"januari", 1, "Januari 2026"},
		{"agustus 2025 BCA", 2, "Agustus 2025"},
		{"15", 0, ""},
		{"10 5 januari", 0, ""},
		{"10 GoPay", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p, used, errMsg := parsePeriod(periodNow, strings.Fields(tt.in))
			if errMsg != "" {
				t.Fatalf("error: %s", errMsg)
			}
			label := ""
			if p != nil {
				label = p.Label
			}
			if used != tt.used || label != tt.label {
				t.Errorf("used %d label %q, want %d %q", used, label, tt.used, tt.label)
			}
		})
	}
}
