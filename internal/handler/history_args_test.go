package handler

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/nurfaizh/keuanganku/internal/model"
)

var historyWallets = []model.Wallet{
	{Name: "BCA"}, {Name: "GoPay"}, {Name: "Jago Kantong Belanja"}, {Name: "007"}, {Name: "Mingguan Kas"},
}

func TestParseHistoryArgs(t *testing.T) {
	tests := []struct {
		in        string
		count     int
		requested int
		period    string // period label, "" = none
		wallet    string
		reply     string // substring of the reply; "" = no reply
	}{
		{"", 10, 10, "", "", ""},
		{"20", 20, 20, "", "", ""},
		{"30", 30, 30, "", "", ""},
		{"50", 30, 50, "", "", ""},
		{"99999999999999999999", 30, 31, "", "", ""},
		{"gopay", 10, 10, "", "GoPay", ""},
		{"jago kantong belanja", 10, 10, "", "Jago Kantong Belanja", ""},
		{"007", 10, 10, "", "007", ""},
		{"10 007", 10, 10, "", "007", ""},
		{"mingguan kas", 10, 10, "", "Mingguan Kas", ""},
		{"mingguan", 10, 10, "16–22 Nov 2026", "", ""},
		{"20 mingguan GoPay", 20, 20, "16–22 Nov 2026", "GoPay", ""},
		{"bulan lalu Jago Kantong Belanja", 10, 10, "Oktober 2026", "Jago Kantong Belanja", ""},
		{"5 januari", 10, 10, "5 Januari 2026", "", ""},
		{"10 5 januari", 10, 10, "5 Januari 2026", "", ""},
		{"15", 15, 15, "", "", ""},
		{"0", 0, 0, "", "", "❌ Format"},
		{"-3", 0, 0, "", "", "❌ Format"},
		{"2.5", 0, 0, "", "", "❌ Format"},
		{"1,5", 0, 0, "", "", "❌ Format"},
		{"BCA 5", 0, 0, "", "", "❌ Format"},
		{"BCA mingguan", 0, 0, "", "", "❌ Format"},
		{"31 feb", 0, 0, "", "", "Tanggal tidak valid"},
		{"Dana", 0, 0, "", "", "Dompet *Dana* tidak ditemukan"},
		{"5 Dana", 0, 0, "", "", "Dompet *Dana* tidak ditemukan"},
		{"Dana 5", 0, 0, "", "", "❌ Format"},
		{"Dana Kantor", 0, 0, "", "", "Dompet *Dana Kantor* tidak ditemukan"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, reply := parseHistoryArgs(periodNow, historyWallets, strings.Fields(tt.in))
			if tt.reply != "" {
				if !strings.Contains(reply, tt.reply) {
					t.Errorf("reply %q, want it to contain %q", reply, tt.reply)
				}
				return
			}
			if reply != "" {
				t.Fatalf("unexpected reply %q", reply)
			}
			label := ""
			if got.Period != nil {
				label = got.Period.Label
			}
			if got.Count != tt.count || got.Requested != tt.requested || label != tt.period || got.Wallet != tt.wallet {
				t.Errorf("got count %d requested %d period %q wallet %q; want %d %d %q %q",
					got.Count, got.Requested, label, got.Wallet, tt.count, tt.requested, tt.period, tt.wallet)
			}
		})
	}
}

// Property 6: a whole wallet name wins, whatever it looks like.
func TestPropertyWholeWalletNameWins(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	words := []string{"5", "januari", "mingguan", "lalu", "2025", "GoPay", "kas", "10", "bulan", "x"}
	for i := 0; i < 1000; i++ {
		var parts []string
		for n := 1 + r.Intn(3); n > 0; n-- {
			w := words[r.Intn(len(words))]
			if r.Intn(2) == 0 {
				w = strings.ToUpper(w)
			}
			parts = append(parts, w)
		}
		name := strings.Join(parts, " ")
		wallets := []model.Wallet{{Name: "BCA"}, {Name: name}}
		got, reply := parseHistoryArgs(periodNow, wallets, strings.Fields(strings.ToLower(name)))
		if reply != "" || got.Wallet != name || got.Period != nil || got.Requested != defaultHistoryCount {
			t.Fatalf("wallet %q: got %+v, reply %q", name, got, reply)
		}
	}
}

func TestHelpListsRiwayat(t *testing.T) {
	help := (&MessageHandler{}).helpMessage()
	if !strings.Contains(help, "\n\n📜 *Riwayat:*\n/riwayat [jumlah] [periode] [dompet]") {
		t.Errorf("help does not list /riwayat as its own block:\n%s", help)
	}
	if !strings.Contains(help, "maksimal 30._\n\n↩️ *Batal:*") {
		t.Errorf("riwayat block not separated from Batal:\n%s", help)
	}
}
