package handler

import (
	"strings"
	"sync"
	"time"

	"github.com/nurfaizh/keuanganku/internal/ai"
	"github.com/nurfaizh/keuanganku/internal/model"
)

// pendingTTL is how long the bot waits for the answer to a follow-up question
// ("Pakai dompet yang mana?"). Long enough to go and check an app, short
// enough that a stray wallet name hours later is not applied to a stale draft.
const pendingTTL = 10 * time.Minute

// pendingStore remembers transactions the bot asked a follow-up question
// about, so the next message can complete them instead of being read cold.
//
// It is in memory on purpose: a draft is only meaningful for minutes, and
// losing one on restart just means the user is asked again. Nothing is
// written to the ledger until the draft is complete and has been run through
// the ordinary command path.
type pendingStore struct {
	mu     sync.Mutex
	drafts map[string]pendingDraft
	now    func() time.Time
}

type pendingDraft struct {
	intent  ai.Intent
	expires time.Time
}

func newPendingStore() *pendingStore {
	return &pendingStore{drafts: make(map[string]pendingDraft), now: time.Now}
}

// pendingKey scopes a draft to one person in one chat. In a group, only the
// member who was asked can answer — another member naming a wallet must not
// complete someone else's expense. senderID may be empty for transports that
// are one-to-one only, in which case the chat is the conversation.
func pendingKey(chatID, senderID string) string {
	return chatID + "|" + senderID
}

func (s *pendingStore) get(key string) (ai.Intent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.drafts[key]
	if !ok {
		return ai.Intent{}, false
	}
	if !s.now().Before(d.expires) {
		delete(s.drafts, key)
		return ai.Intent{}, false
	}
	return d.intent, true
}

func (s *pendingStore) set(key string, intent ai.Intent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	// Sweep abandoned drafts here rather than on a timer: the map only grows
	// when something is stored, so this bounds it without a goroutine.
	for k, d := range s.drafts {
		if !now.Before(d.expires) {
			delete(s.drafts, k)
		}
	}
	s.drafts[key] = pendingDraft{intent: intent, expires: now.Add(pendingTTL)}
}

func (s *pendingStore) clear(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drafts, key)
}

// answerFillers are words people wrap a bare answer in ("pake gopay aja"),
// stripped before checking whether what remains is exactly a wallet or an
// amount.
var answerFillers = map[string]bool{
	"pakai": true, "pake": true, "pk": true, "dari": true, "di": true,
	"dompet": true, "aja": true, "saja": true, "ya": true, "yah": true,
	"yg": true, "yang": true, "dong": true, "deh": true, "kok": true,
	"sih": true, "nya": true, "rp": true, "rp.": true, "lewat": true,
	"via": true, "oke": true, "ok": true,
}

// cancelPhrases discard a pending draft without touching the ledger.
var cancelPhrases = map[string]bool{
	"batal": true, "batalin": true, "gak jadi": true, "ga jadi": true,
	"nggak jadi": true, "ngga jadi": true, "enggak jadi": true,
	"tidak jadi": true, "gajadi": true, "gak usah": true, "ga usah": true,
	"cancel": true, "skip": true,
}

// isCancel reports whether text only asks to drop the pending draft.
func isCancel(text string) bool {
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	t = strings.TrimRight(t, ".!")
	return cancelPhrases[t]
}

// fillFromAnswer completes a draft from a reply that is nothing but the
// missing piece — a wallet name or an amount, optionally wrapped in filler
// ("pake flash aja", "20rb"). It deliberately refuses anything richer: a
// message that also names a purpose may be a brand-new transaction, and that
// call belongs to the model, which sees the draft and the message together.
//
// This path exists so the common case is instant and costs no API quota.
func fillFromAnswer(draft ai.Intent, text string, wallets []model.Wallet) (ai.Intent, bool) {
	var tokens []string
	for _, f := range strings.Fields(text) {
		if !answerFillers[strings.ToLower(strings.Trim(f, ".,!?"))] {
			tokens = append(tokens, strings.Trim(f, ".,!?"))
		}
	}
	if len(tokens) == 0 {
		return draft, false
	}

	switch {
	case draft.Amount <= 0:
		if len(tokens) != 1 {
			return draft, false
		}
		amount, err := parseAmount(strings.TrimPrefix(strings.ToLower(tokens[0]), "rp"))
		if err != nil {
			return draft, false
		}
		draft.Amount = amount

	case draft.Action == ai.ActionTransfer:
		// Only the single-missing-leg case is unambiguous; "bca ke jago"
		// style answers go to the model.
		name := matchWalletName(wallets, strings.Join(tokens, " "))
		if name == "" {
			return draft, false
		}
		switch {
		case draft.Wallet == "" && draft.ToWallet != "":
			draft.Wallet = name
		case draft.Wallet != "" && draft.ToWallet == "":
			draft.ToWallet = name
		default:
			return draft, false
		}

	case draft.Wallet == "":
		name := matchWalletName(wallets, strings.Join(tokens, " "))
		if name == "" {
			return draft, false
		}
		draft.Wallet = name

	default:
		return draft, false
	}

	// The draft may still be incomplete (the amount arrived but the wallet is
	// still missing); the caller checks Command() and asks again if so.
	return draft, true
}
