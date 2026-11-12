package handler

import (
	"testing"
	"time"

	"github.com/nurfaizh/keuanganku/internal/ai"
	"github.com/nurfaizh/keuanganku/internal/model"
)

var testWallets = []model.Wallet{{Name: "jago"}, {Name: "GoPay"}, {Name: "Bank Jago Kantong"}}

// The reported bug: "jajan flash 50k" -> "Pakai dompet apa?" -> "jago" must
// record the 50k, not start a new conversation.
func TestFillFromAnswerWallet(t *testing.T) {
	draft := ai.Intent{Action: ai.ActionExpense, Amount: 50000, Category: "Makanan", Description: "jajan flash"}

	for _, answer := range []string{"jago", "Jago", "pake jago", "pakai jago aja", "jago.", "gopay", "bank jago kantong"} {
		t.Run(answer, func(t *testing.T) {
			got, ok := fillFromAnswer(draft, answer, testWallets)
			if !ok {
				t.Fatalf("fillFromAnswer(%q) did not complete the draft", answer)
			}
			if _, ok := got.Command(); !ok {
				t.Fatalf("filled draft %+v still has no command", got)
			}
			if got.Amount != 50000 || got.Description != "jajan flash" || got.Category != "Makanan" {
				t.Errorf("filling the wallet changed other fields: %+v", got)
			}
		})
	}
}

func TestFillFromAnswerResolvesCanonicalWallet(t *testing.T) {
	draft := ai.Intent{Action: ai.ActionExpense, Amount: 50000}
	got, ok := fillFromAnswer(draft, "GOPAY", testWallets)
	if !ok || got.Wallet != "GoPay" {
		t.Fatalf("got wallet %q ok=%v, want canonical %q", got.Wallet, ok, "GoPay")
	}
}

func TestFillFromAnswerAmount(t *testing.T) {
	draft := ai.Intent{Action: ai.ActionExpense, Wallet: "jago", Description: "bakso"}
	for answer, want := range map[string]float64{"20rb": 20000, "20000": 20000, "rp 20rb": 20000, "Rp20rb": 20000, "1.5jt": 1500000} {
		got, ok := fillFromAnswer(draft, answer, testWallets)
		if !ok || got.Amount != want {
			t.Errorf("fillFromAnswer(%q) = %v ok=%v, want %v", answer, got.Amount, ok, want)
		}
	}
}

// Anything richer than the bare missing piece is left to the model: it may be
// a brand-new transaction that merely mentions a wallet.
func TestFillFromAnswerRefusesRicherMessages(t *testing.T) {
	draft := ai.Intent{Action: ai.ActionExpense, Amount: 50000, Description: "jajan flash"}
	for _, msg := range []string{"beli kopi 20rb jago", "dompet baru", "ovo", "", "makasih ya", "-5rb"} {
		if got, ok := fillFromAnswer(draft, msg, testWallets); ok {
			t.Errorf("fillFromAnswer(%q) completed the draft as %+v; should defer to the model", msg, got)
		}
	}

	noAmount := ai.Intent{Action: ai.ActionExpense, Wallet: "jago"}
	if _, ok := fillFromAnswer(noAmount, "20rb buat bakso", testWallets); ok {
		t.Error("multi-token amount answer should defer to the model")
	}
}

func TestFillFromAnswerTransferLeg(t *testing.T) {
	missingTo := ai.Intent{Action: ai.ActionTransfer, Amount: 100000, Wallet: "jago"}
	got, ok := fillFromAnswer(missingTo, "ke gopay", testWallets)
	// "ke" is not filler on purpose (it marks direction), so this defers.
	if ok {
		t.Fatalf("unexpected deterministic fill %+v", got)
	}
	got, ok = fillFromAnswer(missingTo, "gopay", testWallets)
	if !ok || got.ToWallet != "GoPay" || got.Wallet != "jago" {
		t.Fatalf("got %+v ok=%v, want jago -> GoPay", got, ok)
	}

	missingBoth := ai.Intent{Action: ai.ActionTransfer, Amount: 100000}
	if _, ok := fillFromAnswer(missingBoth, "gopay", testWallets); ok {
		t.Error("a single wallet cannot settle a transfer missing both legs")
	}
}

func TestIsCancel(t *testing.T) {
	for _, s := range []string{"batal", "Gak jadi", "ga  jadi!", "cancel"} {
		if !isCancel(s) {
			t.Errorf("isCancel(%q) = false", s)
		}
	}
	for _, s := range []string{"jago", "batal yang tadi beli kopi", ""} {
		if isCancel(s) {
			t.Errorf("isCancel(%q) = true", s)
		}
	}
}

func TestPendingStoreExpiresAndScopesBySender(t *testing.T) {
	now := time.Date(2026, 11, 12, 13, 0, 0, 0, time.Local)
	s := newPendingStore()
	s.now = func() time.Time { return now }

	draft := ai.Intent{Action: ai.ActionExpense, Amount: 50000}
	s.set(pendingKey("tg:-100", "alice"), draft)

	if _, ok := s.get(pendingKey("tg:-100", "bob")); ok {
		t.Error("another group member must not see alice's draft")
	}
	if _, ok := s.get(pendingKey("tg:-100", "alice")); !ok {
		t.Fatal("alice's draft missing")
	}

	now = now.Add(pendingTTL)
	if _, ok := s.get(pendingKey("tg:-100", "alice")); ok {
		t.Error("draft should expire after pendingTTL")
	}
}
