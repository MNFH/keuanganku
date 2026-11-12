package ai

import (
	"strings"
	"testing"
	"time"
)

// The model can only complete a draft it is shown: the prompt must carry the
// known fields and mark the missing ones.
func TestUserPromptIncludesPendingDraft(t *testing.T) {
	p := &Parser{}
	req := Request{
		Text:    "jago",
		Wallets: []string{"jago", "GoPay"},
		Now:     time.Date(2026, 11, 12, 13, 0, 0, 0, time.Local),
		Pending: &Intent{Action: ActionExpense, Amount: 50000, Description: "jajan flash"},
	}
	got := p.userPrompt(req)
	for _, want := range []string{"DRAFT TERTUNDA", "action: expense", "amount: 50000", "wallet: (BELUM ADA)", "description: jajan flash"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}

	req.Pending = nil
	if strings.Contains(p.userPrompt(req), "DRAFT TERTUNDA") {
		t.Error("prompt mentions a draft when there is none")
	}
}
