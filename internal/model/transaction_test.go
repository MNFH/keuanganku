package model

import "testing"

func TestFormatAmount(t *testing.T) {
	tests := []struct {
		name   string
		amount float64
		want   string
	}{
		{"zero", 0, "0"},
		{"under a thousand", 500, "500"},
		{"exactly a thousand", 1000, "1.000"},
		{"six digits", 500000, "500.000"},
		{"seven digits", 1500000, "1.500.000"},
		{"nine digits", 123456789, "123.456.789"},
		{"rounds to nearest", 1500.7, "1.501"},

		// Regression: the separator loop used to count "-" as a digit, which
		// shifted every separator position for negatives whose digit count is
		// a multiple of three — rendering -500000 as "-.500.000".
		{"negative six digits", -500000, "-500.000"},
		{"negative three digits", -999, "-999"},
		{"negative seven digits", -1500000, "-1.500.000"},
		{"negative two digits", -50, "-50"},
		{"negative nine digits", -123456789, "-123.456.789"},

		// A value that rounds to zero must not render as "-0".
		{"negative rounding to zero", -0.4, "0"},
		{"negative rounding to one", -0.6, "-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatAmount(tt.amount); got != tt.want {
				t.Errorf("FormatAmount(%v) = %q, want %q", tt.amount, got, tt.want)
			}
		})
	}
}

func TestBalanceDelta(t *testing.T) {
	tests := []struct {
		name string
		tx   Transaction
		want float64
	}{
		{"income raises balance", Transaction{Type: Income, Amount: 1000}, 1000},
		{"expense lowers balance", Transaction{Type: Expense, Amount: 1000}, -1000},
		{"transfer out lowers balance", Transaction{Type: Transfer, Direction: DirectionOut, Amount: 1000}, -1000},
		{"transfer in raises balance", Transaction{Type: Transfer, Direction: DirectionIn, Amount: 1000}, 1000},
		{"unknown type is inert", Transaction{Type: TransactionType("bogus"), Amount: 1000}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tx.BalanceDelta(); got != tt.want {
				t.Errorf("BalanceDelta() = %v, want %v", got, tt.want)
			}
		})
	}
}
