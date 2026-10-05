package handler

import (
	"errors"
	"testing"
)

func TestParseAmountAccepts(t *testing.T) {
	tests := []struct {
		in   string
		want float64
	}{
		{"50000", 50000},
		{"50rb", 50000},
		{"50ribu", 50000},
		{"200k", 200000},
		{"2jt", 2000000},
		{"2juta", 2000000},
		{"1.5jt", 1500000},
		{"1.5juta", 1500000},
		{" 50RB ", 50000}, // trimmed and lowercased
		{"0.5rb", 500},    // fractional shorthand
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseAmount(tt.in)
			if err != nil {
				t.Fatalf("parseAmount(%q) returned error %v, want %v", tt.in, err, tt.want)
			}
			if got != tt.want {
				t.Errorf("parseAmount(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// Regression: parseAmount used to return whatever ParseFloat accepted, so a
// negative amount reached the sheet. An expense of -1jt inverts its
// BalanceDelta and *raises* the wallet balance, and NaN/Inf corrupt it
// outright.
func TestParseAmountRejects(t *testing.T) {
	tests := []struct {
		in      string
		wantErr error
	}{
		{"-50000", errAmountNotPositive},
		{"-1jt", errAmountNotPositive},
		{"-0.5rb", errAmountNotPositive},
		{"0", errAmountNotPositive},
		{"0rb", errAmountNotPositive},
		{"NaN", errAmountInvalid},
		{"Inf", errAmountInvalid},
		{"-Inf", errAmountInvalid},
		{"abc", errAmountInvalid},
		{"", errAmountInvalid},
		{"k", errAmountInvalid},
		{"jt", errAmountInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseAmount(tt.in)
			if err == nil {
				t.Fatalf("parseAmount(%q) = %v, want error %v", tt.in, got, tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("parseAmount(%q) error = %v, want %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

// A rejected non-positive amount must say so, rather than falling back to the
// generic "format" message that only lists valid shorthand.
func TestAmountErrorMsg(t *testing.T) {
	if msg := amountErrorMsg("-1jt", errAmountNotPositive); msg != "❌ Nominal harus lebih dari nol: *-1jt*" {
		t.Errorf("unexpected message for non-positive amount: %q", msg)
	}
	if msg := amountErrorMsg("abc", errAmountInvalid); msg == "" || msg == "❌ Nominal harus lebih dari nol: *abc*" {
		t.Errorf("unexpected message for invalid amount: %q", msg)
	}
}
