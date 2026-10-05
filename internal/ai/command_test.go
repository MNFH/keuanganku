package ai

import "testing"

func TestCommand(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
		want   string
	}{
		{
			name: "expense",
			intent: Intent{
				Action: ActionExpense, Amount: 25000, Wallet: "GoPay",
				Category: "Makanan", Description: "nasi goreng",
			},
			want: "/keluar 25000 GoPay Makanan nasi goreng",
		},
		{
			name: "income",
			intent: Intent{
				Action: ActionIncome, Amount: 5000000, Wallet: "BCA",
				Category: "Gaji", Description: "gaji bulan ini",
			},
			want: "/masuk 5000000 BCA Gaji gaji bulan ini",
		},
		{
			// The handler matches the wallet greedily from the front, so a
			// multi-word wallet needs no quoting.
			name: "multi-word wallet",
			intent: Intent{
				Action: ActionExpense, Amount: 20000, Wallet: "Jago Kantong Belanja",
				Category: "Belanja", Description: "sabun",
			},
			want: "/keluar 20000 Jago Kantong Belanja Belanja sabun",
		},
		{
			name: "empty category falls back to Lainnya",
			intent: Intent{
				Action: ActionExpense, Amount: 15000, Wallet: "BCA",
				Description: "parkir",
			},
			want: "/keluar 15000 BCA Lainnya parkir",
		},
		{
			// A multi-word category would eat the first word of the
			// description, since the handler takes one token for category.
			name: "multi-word category is reduced to one token",
			intent: Intent{
				Action: ActionExpense, Amount: 30000, Wallet: "BCA",
				Category: "Makan Siang", Description: "warteg",
			},
			want: "/keluar 30000 BCA Makan warteg",
		},
		{
			// Commands are split with strings.Fields, so a newline in the
			// description would otherwise split the command in two.
			name: "newlines collapse in description",
			intent: Intent{
				Action: ActionExpense, Amount: 10000, Wallet: "BCA",
				Category: "Lainnya", Description: "beli\n  pulsa\n",
			},
			want: "/keluar 10000 BCA Lainnya beli pulsa",
		},
		{
			name: "missing description is omitted",
			intent: Intent{
				Action: ActionExpense, Amount: 5000, Wallet: "BCA", Category: "Lainnya",
			},
			want: "/keluar 5000 BCA Lainnya",
		},
		{
			name: "fractional amount keeps its decimals, no exponent",
			intent: Intent{
				Action: ActionExpense, Amount: 1500.5, Wallet: "BCA", Category: "Lainnya",
			},
			want: "/keluar 1500.5 BCA Lainnya",
		},
		{
			name: "large amount does not become scientific notation",
			intent: Intent{
				Action: ActionIncome, Amount: 1000000000, Wallet: "BCA", Category: "Bonus",
			},
			want: "/masuk 1000000000 BCA Bonus",
		},
		{
			name: "transfer",
			intent: Intent{
				Action: ActionTransfer, Amount: 500000,
				Wallet: "BCA", ToWallet: "GoPay", Description: "uang jajan",
			},
			want: "/transfer 500000 BCA GoPay uang jajan",
		},
		{
			name: "transfer without description",
			intent: Intent{
				Action: ActionTransfer, Amount: 500000, Wallet: "BCA", ToWallet: "GoPay",
			},
			want: "/transfer 500000 BCA GoPay",
		},
		{
			name:   "add wallet uses description as the name",
			intent: Intent{Action: ActionAddWallet, Description: "Jenius"},
			want:   "/dompet tambah Jenius",
		},
		{
			name:   "balance",
			intent: Intent{Action: ActionBalance},
			want:   "/saldo",
		},
		{
			name:   "undo",
			intent: Intent{Action: ActionUndo},
			want:   "/batal",
		},
		{
			name:   "help",
			intent: Intent{Action: ActionHelp},
			want:   "/help",
		},

		// Recap periods map onto the arguments /rekap already parses.
		{"recap this month", Intent{Action: ActionRecap, Period: PeriodThisMonth}, "/rekap"},
		{"recap no period", Intent{Action: ActionRecap}, "/rekap"},
		{"recap last month", Intent{Action: ActionRecap, Period: PeriodLastMonth}, "/rekap bulanan lalu"},
		{"recap this week", Intent{Action: ActionRecap, Period: PeriodThisWeek}, "/rekap mingguan"},
		{"recap last week", Intent{Action: ActionRecap, Period: PeriodLastWeek}, "/rekap mingguan lalu"},
		{"recap today", Intent{Action: ActionRecap, Period: PeriodToday}, "/rekap harian"},
		{"recap yesterday", Intent{Action: ActionRecap, Period: PeriodYesterday}, "/rekap kemarin"},
		{"recap pdf", Intent{Action: ActionRecap, Period: PeriodLastMonth, WantPDF: true}, "/rekap bulanan lalu pdf"},
		{"recap this month pdf", Intent{Action: ActionRecap, WantPDF: true}, "/rekap pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.intent.Command()
			if !ok {
				t.Fatalf("Command() reported no command, want %q", tt.want)
			}
			if got != tt.want {
				t.Errorf("Command() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A misread must not become a command. Anything incomplete is handed back to
// the caller to ask about, because a guessed transaction writes silently to
// the user's ledger.
func TestCommandRefusesIncompleteIntents(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
	}{
		{"unknown action", Intent{Action: ActionUnknown}},
		{"empty action", Intent{}},
		{"unrecognized action", Intent{Action: Action("sideways")}},

		{"expense without wallet", Intent{Action: ActionExpense, Amount: 1000, Category: "Makanan"}},
		{"expense without amount", Intent{Action: ActionExpense, Wallet: "BCA", Category: "Makanan"}},
		{"expense with zero amount", Intent{Action: ActionExpense, Amount: 0, Wallet: "BCA"}},
		{"income without wallet", Intent{Action: ActionIncome, Amount: 1000}},

		// A negative amount would invert its BalanceDelta downstream.
		{"expense with negative amount", Intent{Action: ActionExpense, Amount: -50000, Wallet: "BCA"}},
		{"transfer with negative amount", Intent{Action: ActionTransfer, Amount: -1, Wallet: "BCA", ToWallet: "GoPay"}},

		{"transfer without source", Intent{Action: ActionTransfer, Amount: 1000, ToWallet: "GoPay"}},
		{"transfer without destination", Intent{Action: ActionTransfer, Amount: 1000, Wallet: "BCA"}},
		{"transfer to the same wallet", Intent{Action: ActionTransfer, Amount: 1000, Wallet: "BCA", ToWallet: "BCA"}},
		{"transfer to the same wallet, different case", Intent{Action: ActionTransfer, Amount: 1000, Wallet: "BCA", ToWallet: "bca"}},

		{"add wallet without a name", Intent{Action: ActionAddWallet}},
		{"add wallet with blank name", Intent{Action: ActionAddWallet, Description: "   "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := tt.intent.Command(); ok {
				t.Errorf("Command() = %q, ok=true; want no command", got)
			}
		})
	}
}

func TestMutates(t *testing.T) {
	mutating := []Action{ActionExpense, ActionIncome, ActionTransfer}
	for _, a := range mutating {
		if !(Intent{Action: a}).Mutates() {
			t.Errorf("%s should be reported as mutating", a)
		}
	}

	readOnly := []Action{
		ActionBalance, ActionRecap, ActionHelp, ActionUnknown,
		// /batal mutates, but it IS the undo — hinting at it would be circular.
		ActionUndo,
		// Adding a wallet is not undoable via /batal, so no hint.
		ActionAddWallet,
	}
	for _, a := range readOnly {
		if (Intent{Action: a}).Mutates() {
			t.Errorf("%s should not be reported as mutating", a)
		}
	}
}
