package sheets

import (
	"reflect"
	"testing"

	"github.com/nurfaizh/keuanganku/internal/model"
)

func tx(id, ref string, canceled bool) *model.Transaction {
	return &model.Transaction{ID: id, RefID: ref, Canceled: canceled}
}

// Repeated /batal must walk back through history: canceled rows are skipped
// so the second /batal reaches the entry before, not the same row again.
func TestLastActiveEntry(t *testing.T) {
	tests := []struct {
		name string
		txs  []*model.Transaction
		want []int
	}{
		{"empty", nil, nil},
		{"single active", []*model.Transaction{tx("a", "", false)}, []int{0}},
		{"last one canceled walks back", []*model.Transaction{tx("a", "", false), tx("b", "", false), tx("c", "", true)}, []int{1}},
		{"all canceled", []*model.Transaction{tx("a", "", true), tx("b", "", true)}, nil},
		{"malformed rows skipped", []*model.Transaction{tx("a", "", false), nil}, []int{0}},
		{"transfer legs together", []*model.Transaction{tx("x", "", false), tx("out", "r1", false), tx("in", "r1", false)}, []int{2, 1}},
		{
			"canceled transfer skipped to earlier entry",
			[]*model.Transaction{tx("x", "", false), tx("out", "r1", true), tx("in", "r1", true)},
			[]int{0},
		},
		{
			"transfer then later canceled expense",
			[]*model.Transaction{tx("out", "r1", false), tx("in", "r1", false), tx("e", "", true)},
			[]int{1, 0},
		},
		{"orphan transfer leg alone", []*model.Transaction{tx("x", "", false), tx("in", "r9", false)}, []int{1}},
		{
			"pair must be active",
			[]*model.Transaction{tx("out", "r1", true), tx("in", "r1", false)},
			[]int{1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastActiveEntry(tt.txs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("lastActiveEntry = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseTransactionRowCanceled(t *testing.T) {
	base := []interface{}{"id", "2026-11-12 13:00:00", "expense", "50000", "jago", "Makanan", "jajan", "", ""}

	got, err := parseTransactionRow(base)
	if err != nil || got.Canceled {
		t.Fatalf("legacy 9-column row: canceled=%v err=%v, want active", got.Canceled, err)
	}

	got, _ = parseTransactionRow(append(append([]interface{}{}, base...), ""))
	if got.Canceled {
		t.Error("empty column J must mean active")
	}

	got, _ = parseTransactionRow(append(append([]interface{}{}, base...), "2026-11-12 14:00:00"))
	if !got.Canceled {
		t.Error("filled column J must mean canceled")
	}
}
