package sheets

import (
	"strings"
	"testing"

	"github.com/nurfaizh/keuanganku/internal/model"
)

func tx(id string, typ model.TransactionType, amount float64, wallet string) model.Transaction {
	return model.Transaction{ID: id, Type: typ, Amount: amount, Wallet: wallet}
}

func leg(id, ref string, dir model.Direction, amount float64, wallet string) model.Transaction {
	t := tx(id, model.Transfer, amount, wallet)
	t.RefID, t.Direction = ref, dir
	return t
}

func ids(es []entry) string {
	var s []string
	for _, e := range es {
		s = append(s, e.tx.ID)
	}
	return strings.Join(s, ",")
}

func TestLastActive(t *testing.T) {
	tests := []struct {
		name    string
		entries []entry
		want    string
	}{
		{"empty sheet", nil, ""},
		{"single", []entry{
			{tx: tx("a", model.Expense, 10, "jago"), row: 2},
			{tx: tx("b", model.Income, 20, "jago"), row: 3},
		}, "b"},
		{"skips cancelled rows at the end", []entry{
			{tx: tx("a", model.Expense, 10, "jago"), row: 2},
			{tx: tx("b", model.Income, 20, "jago"), row: 3, cancelled: true},
		}, "a"},
		{"everything cancelled", []entry{
			{tx: tx("a", model.Expense, 10, "jago"), row: 2, cancelled: true},
		}, ""},
		{"transfer takes both legs", []entry{
			{tx: tx("a", model.Expense, 10, "jago"), row: 2},
			{tx: leg("out", "r1", model.DirectionOut, 50, "jago"), row: 3},
			{tx: leg("in", "r1", model.DirectionIn, 50, "bsi"), row: 4},
		}, "in,out"},
		// With several chats appending, another row can land between the legs
		// of an older transfer; the pair is found by RefID, not position.
		{"transfer legs need not be adjacent", []entry{
			{tx: leg("out", "r1", model.DirectionOut, 50, "jago"), row: 2},
			{tx: tx("x", model.Expense, 10, "bsi"), row: 3, cancelled: true},
			{tx: leg("in", "r1", model.DirectionIn, 50, "bsi"), row: 4},
		}, "in,out"},
		{"an already-cancelled leg is not cancelled again", []entry{
			{tx: leg("out", "r1", model.DirectionOut, 50, "jago"), row: 2, cancelled: true},
			{tx: leg("in", "r1", model.DirectionIn, 50, "bsi"), row: 3},
		}, "in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ids(lastActive(tt.entries)); got != tt.want {
				t.Errorf("lastActive = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidWalletName(t *testing.T) {
	for _, name := range []string{"jago", "BCA", "Jago Kantong Belanja", "007", "GoPay-2", "bank_dki", "Émoney"} {
		if err := validWalletName(name); err != nil {
			t.Errorf("validWalletName(%q) = %v, want ok", name, err)
		}
	}
	for _, name := range []string{
		"", "=bsi", "<x", ">x", "+62", "-cash", "'jago", " jago", "@dana", "#1",
		"go*pay", "dana?", "a~b", // wildcards anywhere
	} {
		if err := validWalletName(name); err == nil {
			t.Errorf("validWalletName(%q) accepted, want rejected", name)
		}
	}
}

func TestOpeningBalance(t *testing.T) {
	txs := []entry{
		{tx: tx("1", model.Income, 1000, "jago")},
		{tx: tx("2", model.Expense, 300, "Jago")}, // SUMIFS matches case-insensitively
		{tx: leg("3", "r", model.DirectionOut, 200, "jago")},
		{tx: leg("4", "r", model.DirectionIn, 200, "bsi")},
		{tx: tx("5", model.Expense, 999, "jago"), cancelled: true},
		{tx: tx("6", model.Income, 50, "bsi")},
	}

	// jago net = +1000 -300 -200 = +500, so a typed 2500 opens at 2000.
	if got := openingBalance(2500, "jago", txs); got != 2000 {
		t.Errorf("jago opening = %v, want 2000", got)
	}
	// bsi net = +200 +50 = +250.
	if got := openingBalance(250, "bsi", txs); got != 0 {
		t.Errorf("bsi opening = %v, want 0", got)
	}
	if got := openingBalance(700, "mandiri", txs); got != 700 {
		t.Errorf("wallet with no transactions: opening = %v, want 700", got)
	}
}

// The formula and BalanceDelta must agree, or EnsureBalanceFormulas would
// back out the wrong opening balance. Check the formula counts each case
// BalanceDelta does, with the same sign, and skips cancelled rows.
func TestBalanceFormulaMatchesBalanceDelta(t *testing.T) {
	for _, want := range []string{
		`=INDEX($D:$D,ROW())`,
		`+SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"income",Transactions!$J:$J,"")`,
		`-SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"expense",Transactions!$J:$J,"")`,
		`+SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"transfer",Transactions!$I:$I,"in",Transactions!$J:$J,"")`,
		`-SUMIFS(Transactions!$D:$D,Transactions!$E:$E,INDEX($A:$A,ROW()),Transactions!$C:$C,"transfer",Transactions!$I:$I,"out",Transactions!$J:$J,"")`,
	} {
		if !strings.Contains(balanceFormula, want) {
			t.Errorf("balanceFormula is missing %s", want)
		}
	}
	for typ, sign := range map[string]float64{
		string(model.Income): 1, string(model.Expense): -1,
	} {
		if d := tx("", model.TransactionType(typ), 1, "").BalanceDelta(); d != sign {
			t.Errorf("BalanceDelta(%s) = %v, formula uses %v", typ, d, sign)
		}
	}
	if d := leg("", "", model.DirectionIn, 1, "").BalanceDelta(); d != 1 {
		t.Errorf("BalanceDelta(transfer in) = %v, formula uses +1", d)
	}
	if d := leg("", "", model.DirectionOut, 1, "").BalanceDelta(); d != -1 {
		t.Errorf("BalanceDelta(transfer out) = %v, formula uses -1", d)
	}
}
