package sheets

import (
	"strings"
	"testing"

	"github.com/nurfaizh/keuanganku/internal/model"
)

func TestBalanceFormulaShape(t *testing.T) {
	f := balanceFormula(5)
	if !strings.HasPrefix(f, "=$D5+SUMIFS(") {
		t.Errorf("formula should start from the opening balance on its own row: %s", f)
	}
	if strings.Count(f, "SUMIFS(") != 4 {
		t.Errorf("want 4 SUMIFS terms (income, expense, transfer in, transfer out): %s", f)
	}
	if strings.Count(f, `Transactions!$J:$J,""`) != 4 {
		t.Errorf("every term must skip canceled rows: %s", f)
	}
	if strings.Count(f, "$A5") != 4 || strings.Contains(f, "$A6") {
		t.Errorf("every term must reference its own row's wallet name: %s", f)
	}
	if strings.Contains(f, ";") {
		t.Errorf("the API takes en_US formulas; ';' separators would break: %s", f)
	}
}

// For every well-formed row the formula must agree with BalanceDelta, the
// sign rule the rest of the code uses; otherwise /saldo and recaps disagree.
func TestFormulaDeltaMatchesBalanceDelta(t *testing.T) {
	txs := []model.Transaction{
		{Type: model.Income, Amount: 1000},
		{Type: model.Expense, Amount: 1000},
		{Type: model.Transfer, Direction: model.DirectionIn, Amount: 1000},
		{Type: model.Transfer, Direction: model.DirectionOut, Amount: 1000},
	}
	for _, tx := range txs {
		if got, want := formulaDelta(tx), tx.BalanceDelta(); got != want {
			t.Errorf("%s/%s: formulaDelta = %v, BalanceDelta = %v", tx.Type, tx.Direction, got, want)
		}
	}
}

func TestFormulaDeltaEdgeCases(t *testing.T) {
	if d := formulaDelta(model.Transaction{Type: model.Expense, Amount: 500, Canceled: true}); d != 0 {
		t.Errorf("canceled row counted: %v", d)
	}
	// SUMIFS matches "transfer" with no direction against neither "in" nor
	// "out", so migration must count it as nothing too.
	if d := formulaDelta(model.Transaction{Type: model.Transfer, Amount: 500}); d != 0 {
		t.Errorf("direction-less transfer counted: %v", d)
	}
	if d := formulaDelta(model.Transaction{Type: "INCOME", Amount: 500}); d != 500 {
		t.Errorf("SUMIFS is case-insensitive, so formulaDelta must be: %v", d)
	}
}

func TestNeedsFormula(t *testing.T) {
	cases := map[string]struct {
		cell interface{}
		want bool
	}{
		"legacy number":     {"150000", true},
		"legacy float":      {150000.0, true},
		"empty":             {"", true},
		"nil":               {nil, true},
		"our formula":       {balanceFormula(2), false},
		"user formula":      {"=1000+500", false},
		"broken by deleted": {`=$D2+SUMIFS(#REF!,#REF!,$A2)`, true},
	}
	for name, c := range cases {
		if got := needsFormula(c.cell); got != c.want {
			t.Errorf("%s: needsFormula(%v) = %v, want %v", name, c.cell, got, c.want)
		}
	}
}

func TestValidateWalletName(t *testing.T) {
	for _, ok := range []string{"BCA", "Bank Jago Kantong", "GoPay 2", "jago"} {
		if err := ValidateWalletName(ok); err != nil {
			t.Errorf("ValidateWalletName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "Ga*ji", "ovo?", "a~b", "=x", ">10", "<a"} {
		if ValidateWalletName(bad) == nil {
			t.Errorf("ValidateWalletName(%q) accepted a name SUMIFS would misread", bad)
		}
	}
}

func TestCellNumber(t *testing.T) {
	cases := []struct {
		row  []interface{}
		want float64
		ok   bool
	}{
		{[]interface{}{"jago", 1500.0}, 1500, true},
		{[]interface{}{"jago", "1500"}, 1500, true},
		{[]interface{}{"jago"}, 0, true},     // trailing empty cell is omitted by the API
		{[]interface{}{"jago", ""}, 0, true}, // empty balance
		{[]interface{}{"jago", "#REF!"}, 0, false},
		{[]interface{}{"jago", "#NAME?"}, 0, false},
	}
	for _, c := range cases {
		got, ok := cellNumber(c.row, 1)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("cellNumber(%v) = %v,%v want %v,%v", c.row, got, ok, c.want, c.ok)
		}
	}
}
