package ai

import "testing"

// The API rejects "" as an enum member, so optional fields carry the "none"
// sentinel on the wire. Everything downstream expects the zero value.
func TestNormalize(t *testing.T) {
	in := Intent{
		Action:      ActionBalance,
		Wallet:      sentinelNone,
		ToWallet:    sentinelNone,
		Category:    sentinelNone,
		Period:      Period(sentinelNone),
		Description: sentinelNone,
	}
	in.normalize()

	if in.Wallet != "" {
		t.Errorf("Wallet = %q, want empty", in.Wallet)
	}
	if in.ToWallet != "" {
		t.Errorf("ToWallet = %q, want empty", in.ToWallet)
	}
	if in.Category != "" {
		t.Errorf("Category = %q, want empty", in.Category)
	}
	if in.Period != PeriodNone {
		t.Errorf("Period = %q, want empty", in.Period)
	}
	// Otherwise the sheet gets a transaction described as "none".
	if in.Description != "" {
		t.Errorf("Description = %q, want empty", in.Description)
	}
}

func TestNormalizeLeavesRealValuesAlone(t *testing.T) {
	in := Intent{
		Action:      ActionExpense,
		Amount:      25000,
		Wallet:      "GoPay",
		Category:    "Makanan",
		Description: "nasi goreng",
		Period:      PeriodLastMonth,
	}
	want := in
	in.normalize()

	if in != want {
		t.Errorf("normalize() changed a complete intent:\n got %+v\nwant %+v", in, want)
	}
}

// A wallet or description that merely contains the sentinel as a substring
// must survive untouched.
func TestNormalizeOnlyMatchesTheWholeValue(t *testing.T) {
	in := Intent{Wallet: "none-of-your-business", Description: "nonetheless penting"}
	in.normalize()

	if in.Wallet != "none-of-your-business" {
		t.Errorf("Wallet = %q, want it unchanged", in.Wallet)
	}
	if in.Description != "nonetheless penting" {
		t.Errorf("Description = %q, want it unchanged", in.Description)
	}
}

// A "none" wallet_text would otherwise render "/riwayat none" and answer
// "wallet none not found".
func TestNormalizeHistoryFields(t *testing.T) {
	in := Intent{Action: ActionHistory, WalletText: "None", Count: -5}
	in.normalize()
	if in.WalletText != "" || in.Count != 0 {
		t.Errorf("got WalletText %q Count %d, want empty and 0", in.WalletText, in.Count)
	}
	if cmd, _ := in.Command(); cmd != "/riwayat" {
		t.Errorf("Command() = %q, want /riwayat", cmd)
	}

	real := Intent{Action: ActionHistory, WalletText: "Dana", Count: 7}
	real.normalize()
	if real.WalletText != "Dana" || real.Count != 7 {
		t.Errorf("normalize changed real values: %+v", real)
	}
}
