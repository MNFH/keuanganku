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
