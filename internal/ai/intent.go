// Package ai turns free-form WhatsApp messages — and photos of receipts — into
// the slash commands the bot already implements, so users can write "beli nasi
// goreng 25rb pakai gopay" instead of memorising command syntax.
//
// The model never touches the spreadsheet. It only classifies a message into a
// structured Intent; the handler renders that as a canonical command and runs
// it through the normal command path. Keeping the model on the parsing side of
// that boundary means every existing rule — amount validation, wallet
// matching, balance bookkeeping, rollback — still applies exactly once, and
// the bot degrades to slash-commands-only when the AI is unavailable.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
)

// Action is the kind of request a message was understood to be.
type Action string

const (
	ActionExpense   Action = "expense"
	ActionIncome    Action = "income"
	ActionTransfer  Action = "transfer"
	ActionAddWallet Action = "add_wallet"
	ActionBalance   Action = "balance"
	ActionRecap     Action = "recap"
	ActionUndo      Action = "undo"
	ActionHelp      Action = "help"
	// ActionUnknown means the message wasn't a finance request the bot can
	// act on. See Intent.Reply for how the caller should respond.
	ActionUnknown Action = "unknown"
)

// sentinelNone is the enum member meaning "not applicable".
//
// The API rejects an empty string as an enum value ("enum[0]: cannot be
// empty"), so optional enum-typed fields offer this instead and are
// normalised back to "" by Intent.normalize once decoded. Keeping every field
// required and enum-constrained means the model always returns a shape we can
// decode, rather than us relying on it to omit a field.
const sentinelNone = "none"

// Period is a recap window, mapped to the existing /rekap arguments by
// Intent.Command.
type Period string

const (
	PeriodNone      Period = ""
	PeriodThisMonth Period = "bulan_ini"
	PeriodLastMonth Period = "bulan_lalu"
	PeriodThisWeek  Period = "minggu_ini"
	PeriodLastWeek  Period = "minggu_lalu"
	PeriodToday     Period = "hari_ini"
	PeriodYesterday Period = "kemarin"
)

// Categories is the fixed category vocabulary offered to the model.
//
// It is a closed set rather than free text on purpose: the sheet's category
// breakdown and the PDF report's chart only group usefully when the same
// spending lands under the same label every time. A free-text category would
// scatter "Makan", "makanan", and "Food" across three rows.
var Categories = []string{
	"Makanan",
	"Transport",
	"Belanja",
	"Tagihan",
	"Hiburan",
	"Kesehatan",
	"Pendidikan",
	"Gaji",
	"Bonus",
	"Hadiah",
	"Investasi",
	"Lainnya",
}

// Intent is the model's structured reading of one message.
type Intent struct {
	Action      Action  `json:"action"`
	Amount      float64 `json:"amount"`
	Wallet      string  `json:"wallet"`
	ToWallet    string  `json:"to_wallet"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Period      Period  `json:"period"`
	WantPDF     bool    `json:"want_pdf"`

	// Reply is what to say back when Action is ActionUnknown. It is empty for
	// ordinary conversation that has nothing to do with money — the caller
	// stays silent in that case rather than replying to every chat message —
	// and non-empty when the message looked like a finance request but was
	// missing something the bot needs.
	Reply string `json:"reply"`
}

// Parser classifies messages using the Gemini API.
type Parser struct {
	client *genai.Client
	model  string
}

// New builds a Parser against the Gemini Developer API.
func New(ctx context.Context, apiKey, model string) (*Parser, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini api key is empty")
	}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini client: %w", err)
	}
	return &Parser{client: client, model: model}, nil
}

// Request is one message to classify, plus the context the model needs to
// resolve it: which wallets exist, and what "today" means.
type Request struct {
	Text string

	// Image is an optional receipt photo. ImageMIME must be set alongside it
	// (e.g. "image/jpeg"). Text then holds the image's caption, if any.
	Image     []byte
	ImageMIME string

	// Wallets are the user's wallet names, exactly as stored in the sheet.
	// The model is restricted to choosing from these.
	Wallets []string

	// Now anchors relative dates like "kemarin", in the user's local time.
	Now time.Time
}

// Parse classifies one message into an Intent.
func (p *Parser) Parse(ctx context.Context, req Request) (Intent, error) {
	parts := []*genai.Part{{Text: p.userPrompt(req)}}
	if len(req.Image) > 0 {
		mime := req.ImageMIME
		if mime == "" {
			mime = "image/jpeg"
		}
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{Data: req.Image, MIMEType: mime},
		})
	}

	var temperature float32 // 0 — this is extraction, not writing
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
		Temperature:      &temperature,
		ResponseMIMEType: "application/json",
		ResponseSchema:   responseSchema(req.Wallets),
		MaxOutputTokens:  1024,
	}

	resp, err := p.generate(ctx, []*genai.Content{{Parts: parts}}, cfg)
	if err != nil {
		return Intent{}, fmt.Errorf("gemini generate: %w", err)
	}

	raw := strings.TrimSpace(resp.Text())
	if raw == "" {
		return Intent{}, fmt.Errorf("gemini returned no content")
	}

	var intent Intent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return Intent{}, fmt.Errorf("decode intent %q: %w", raw, err)
	}
	intent.normalize()
	return intent, nil
}

// Retry settings for transient API failures. The free tier's rate limit is
// low enough (a handful of requests per minute) that a burst of messages can
// trip it, and the service also returns short-lived 503s under load. Without
// a retry those surface as the bot silently ignoring a message.
//
// Worst case is baseBackoff + 2*baseBackoff plus three request latencies,
// which stays inside the caller's per-message timeout.
const (
	maxAttempts = 3
	baseBackoff = 2 * time.Second
)

// generate calls the API, retrying transient failures with exponential
// backoff. It gives up immediately on anything permanent, such as a bad
// schema or a rejected key, since retrying those only wastes quota.
func (p *Parser) generate(ctx context.Context, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(baseBackoff << (attempt - 1)):
			}
		}

		resp, err := p.client.Models.GenerateContent(ctx, p.model, contents, cfg)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

// retryable reports whether an error is worth another attempt: rate limiting
// and the 5xx family, which the service documents as temporary.
func retryable(err error) bool {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// normalize converts the "not applicable" sentinel back to empty strings, so
// the rest of the package can treat an absent wallet, category or period as
// the zero value.
func (in *Intent) normalize() {
	if in.Wallet == sentinelNone {
		in.Wallet = ""
	}
	if in.ToWallet == sentinelNone {
		in.ToWallet = ""
	}
	if in.Category == sentinelNone {
		in.Category = ""
	}
	if in.Period == Period(sentinelNone) {
		in.Period = PeriodNone
	}
	// Description is free text, not an enum, but the model still reaches for
	// the sentinel when there is nothing to describe. Left alone it would be
	// written into the sheet as a transaction literally described as "none".
	if strings.EqualFold(in.Description, sentinelNone) {
		in.Description = ""
	}
}

// userPrompt carries the per-request context: the wallet list, today's date,
// and the message itself.
func (p *Parser) userPrompt(req Request) string {
	var b strings.Builder

	b.WriteString("Dompet yang tersedia (gunakan nama persis seperti ini):\n")
	if len(req.Wallets) == 0 {
		b.WriteString("(belum ada dompet)\n")
	}
	for _, w := range req.Wallets {
		fmt.Fprintf(&b, "- %s\n", w)
	}

	fmt.Fprintf(&b, "\nHari ini: %s (%s)\n",
		req.Now.Format("2006-01-02"), indoWeekday(req.Now))

	if len(req.Image) > 0 {
		b.WriteString("\nPesan berisi FOTO STRUK/NOTA.")
		if strings.TrimSpace(req.Text) != "" {
			fmt.Fprintf(&b, " Caption: %q", req.Text)
		} else {
			b.WriteString(" Tidak ada caption.")
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "\nPesan pengguna:\n%q\n", req.Text)
	}

	return b.String()
}

var indoWeekdays = [...]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}

func indoWeekday(t time.Time) string { return indoWeekdays[int(t.Weekday())] }

const systemPrompt = `You classify Indonesian WhatsApp messages for a personal finance bot into a structured intent. Reply with JSON matching the schema — nothing else.

ACTIONS
- expense: user spent money. ("beli bakso 20rb gopay", "bayar listrik 300rb bca")
- income: user received money. ("gajian 5jt bca", "dapet bonus 1jt")
- transfer: money moved between the user's OWN wallets. Needs both wallet and to_wallet. ("pindah 500rb dari bca ke gopay")
- add_wallet: user wants a new wallet. Put the name in description. ("tambah dompet Jenius")
- balance: user asks their balance or a summary. ("saldo berapa", "sisa duit")
- recap: user asks a report for a period. Set period, and want_pdf if they ask for a PDF/file. ("rekap bulan ini", "laporan minggu lalu pdf")
- undo: user wants to cancel the last entry. ("batal", "salah, hapus")
- help: user asks what the bot can do.
- unknown: anything else. See the REPLY rules.

AMOUNTS
Always resolve Indonesian shorthand to a plain number: "25rb"/"25k"/"25 ribu" = 25000, "1.5jt"/"1,5 juta" = 1500000, "300rb" = 300000. Amount must be a positive number. Never return a negative amount — an expense is already a reduction.

NOT APPLICABLE
For wallet, to_wallet, category and period, use the literal value "none" when the field does not apply to this message. Never leave them blank.

WALLETS
Use a wallet name EXACTLY as listed, matching case. Map casually-typed names to the listed one ("gopay" -> "GoPay", "bca" -> "BCA"). Wallet names may contain spaces. If the message names no wallet, or names one that is not on the list, use "none" — never substitute a different wallet.

CATEGORY
Pick the closest from the allowed list. Use "Lainnya" when nothing fits but it is still income or an expense. Income is usually "Gaji", "Bonus", "Hadiah", or "Investasi".

DESCRIPTION
A short human label for the entry, in Indonesian — what was bought, or the merchant. Keep it on one line. Never include the amount or the wallet name in the description.

RECEIPT PHOTOS
When the message is a receipt photo, read the GRAND TOTAL — the final amount paid after any discount and tax. It is not the subtotal, not a single line item, and not the cash tendered ("TUNAI") or the change ("KEMBALI"). Use the merchant/shop name as the description. Treat it as an expense.

Infer the category from what was bought and the kind of merchant: a minimarket or grocery run is "Belanja", or "Makanan" when it is nearly all food and drink; a restaurant or cafe is "Makanan"; a pharmacy or clinic is "Kesehatan"; a fuel station, parking or ride-hailing is "Transport"; a utility or phone bill is "Tagihan". Reach for "Lainnya" only when the receipt genuinely gives you nothing to go on — defaulting to it makes the user's spending breakdown useless.

If the caption names a wallet or a category, prefer the caption. If the image is not a receipt at all, return unknown.

REPLY RULES
Leave reply EMPTY for every action except unknown. When you have understood the request, the bot phrases its own confirmation — a reply there is discarded and only confuses the log.

For action = unknown:
- Ordinary conversation with nothing to do with money ("halo", "oke", "makasih", chit-chat): leave reply EMPTY. The bot will stay silent, which matters because it also sits in group chats.
- Looks like a finance request but something essential is missing or ambiguous (no amount, or a wallet you cannot resolve): set reply to a short Indonesian question asking only for what is missing. When you already read an amount or a merchant — from a receipt especially — state them in the question ("Total Rp 183.150 dari Toko Berkah Jaya. Pakai dompet apa?") so the user can catch a misread before anything is recorded.
- Never invent an amount, a wallet, or a transaction you are not confident about. Returning unknown is always better than guessing — a wrong entry silently corrupts the user's ledger.`

// responseSchema describes the Intent JSON. Wallet fields are restricted to
// the user's actual wallets (plus "") so the model cannot invent one.
func responseSchema(wallets []string) *genai.Schema {
	// Enum members are the user's real wallets plus the sentinel, so the model
	// cannot invent a wallet and has an explicit way to say "none named".
	walletSchema := func(desc string) *genai.Schema {
		return &genai.Schema{
			Type:        genai.TypeString,
			Enum:        append([]string{sentinelNone}, wallets...),
			Description: desc,
		}
	}

	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"action": {
				Type: genai.TypeString,
				Enum: []string{
					string(ActionExpense), string(ActionIncome), string(ActionTransfer),
					string(ActionAddWallet), string(ActionBalance), string(ActionRecap),
					string(ActionUndo), string(ActionHelp), string(ActionUnknown),
				},
				Description: "What the user wants to do.",
			},
			"amount": {
				Type:        genai.TypeNumber,
				Description: "Positive rupiah amount, shorthand already resolved. 0 when not applicable.",
			},
			"wallet":    walletSchema(`Wallet involved, or the source wallet for a transfer. "none" if the message names no wallet you can resolve.`),
			"to_wallet": walletSchema(`Destination wallet for a transfer. "none" otherwise.`),
			"category": {
				Type:        genai.TypeString,
				Enum:        append([]string{sentinelNone}, Categories...),
				Description: `Category for income/expense. "none" otherwise.`,
			},
			"description": {
				Type:        genai.TypeString,
				Description: "Short one-line Indonesian label, or the new wallet's name for add_wallet.",
			},
			"period": {
				Type: genai.TypeString,
				Enum: []string{
					sentinelNone, string(PeriodThisMonth), string(PeriodLastMonth),
					string(PeriodThisWeek), string(PeriodLastWeek),
					string(PeriodToday), string(PeriodYesterday),
				},
				Description: `Recap window. "none" unless action is recap.`,
			},
			"want_pdf": {
				Type:        genai.TypeBoolean,
				Description: "True only when the user asked for a PDF or a file.",
			},
			"reply": {
				Type:        genai.TypeString,
				Description: "For unknown: an Indonesian question, or empty to stay silent.",
			},
		},
		Required: []string{
			"action", "amount", "wallet", "to_wallet",
			"category", "description", "period", "want_pdf", "reply",
		},
		PropertyOrdering: []string{
			"action", "amount", "wallet", "to_wallet",
			"category", "description", "period", "want_pdf", "reply",
		},
	}
}
