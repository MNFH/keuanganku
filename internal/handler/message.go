package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/nurfaizh/keuanganku/internal/ai"
	"github.com/nurfaizh/keuanganku/internal/model"
	"github.com/nurfaizh/keuanganku/internal/recap"
	"github.com/nurfaizh/keuanganku/internal/report"
	"github.com/nurfaizh/keuanganku/internal/sheets"
	"github.com/nurfaizh/keuanganku/internal/userstore"
)

// IntentParser turns a free-form message into a structured intent. It is an
// interface so the handler stays testable and provider-agnostic — the bot
// works with it absent, just without natural-language input.
type IntentParser interface {
	Parse(ctx context.Context, req ai.Request) (ai.Intent, error)
}

type MessageHandler struct {
	users       *userstore.Store
	credentials string
	cache       map[string]*sheets.Client
	cacheMu     sync.RWMutex

	// parser is nil when no AI credentials are configured, in which case
	// free-form messages and receipt photos are ignored.
	parser IntentParser

	// pending holds transactions awaiting the answer to a follow-up question.
	pending *pendingStore

	// MonthlyReports mirrors whether the scheduler is running, so /help only
	// promises the automatic report when it will actually arrive.
	MonthlyReports bool
}

// Reply is what Handle wants sent back. If Document is non-nil it should be
// sent as a file attachment (with Caption as the caption); otherwise Text is
// sent as a plain message. A zero-value Reply means "send nothing".
type Reply struct {
	Text     string
	Document []byte
	Filename string
	Caption  string
}

// Message is one inbound chat message. Image is set for a photo, in which
// case Text holds its caption.
type Message struct {
	Text      string
	Image     []byte
	ImageMIME string

	// SenderID is a stable ID for the person who sent the message, used to
	// keep a follow-up question in a group chat tied to whoever was asked.
	// Empty for one-to-one-only transports, where the chat is the person.
	SenderID string
}

// New builds a handler. parser may be nil to run with slash commands only.
func New(users *userstore.Store, credentials string, parser IntentParser) *MessageHandler {
	return &MessageHandler{
		users:       users,
		credentials: credentials,
		cache:       make(map[string]*sheets.Client),
		parser:      parser,
		pending:     newPendingStore(),
	}
}

// Handle processes one message and returns what to send back.
//
// Slash commands are dispatched directly. Anything else — prose, or a receipt
// photo — goes to the intent parser, which produces a command that is then run
// through the very same dispatch, so natural-language input cannot bypass any
// validation that typed input has to pass.
func (h *MessageHandler) Handle(ctx context.Context, chatJID string, msg Message) Reply {
	text := strings.TrimSpace(msg.Text)

	if len(msg.Image) == 0 && strings.HasPrefix(text, "/") {
		// An explicit command supersedes any half-finished conversation.
		h.pending.clear(pendingKey(chatJID, msg.SenderID))
		return h.handleCommand(ctx, chatJID, text)
	}
	if h.parser == nil || (len(msg.Image) == 0 && text == "") {
		return Reply{}
	}
	return h.handleNaturalLanguage(ctx, chatJID, msg)
}

func (h *MessageHandler) handleCommand(ctx context.Context, senderJID, text string) Reply {
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	// Registration command — no sheet needed
	if cmd == "/daftar" {
		return Reply{Text: h.cmdDaftar(ctx, senderJID, args)}
	}
	if cmd == "/help" || cmd == "/bantuan" {
		return Reply{Text: h.helpMessage()}
	}

	// All other commands require a registered sheet
	sheetsClient, err := h.getSheetsClient(senderJID)
	if err != nil {
		return Reply{Text: "❌ Kamu belum terdaftar. Kirim:\n*/daftar <spreadsheet_id>*\n\nCara dapat ID: buka Google Sheet kamu, salin ID dari URL-nya."}
	}

	switch cmd {
	case "/masuk":
		return Reply{Text: h.cmdMasuk(ctx, sheetsClient, args)}
	case "/keluar":
		return Reply{Text: h.cmdKeluar(ctx, sheetsClient, args)}
	case "/dompet":
		return Reply{Text: h.cmdDompet(ctx, sheetsClient, args)}
	case "/transfer":
		return Reply{Text: h.cmdTransfer(ctx, sheetsClient, args)}
	case "/summary", "/ringkasan", "/saldo":
		return Reply{Text: h.cmdSummary(ctx, sheetsClient)}
	case "/rekap":
		return h.cmdRekap(ctx, sheetsClient, args)
	case "/batal":
		return Reply{Text: h.cmdBatal(ctx, sheetsClient)}
	case "/hapus":
		return Reply{Text: h.cmdHapus(senderJID)}
	default:
		return Reply{Text: fmt.Sprintf("❓ Perintah *%s* tidak dikenal.\n\n", cmd) + h.helpMessage()}
	}
}

// handleNaturalLanguage parses a free-form message or receipt photo into a
// command and runs it.
//
// It stays silent (a zero Reply) for anything it cannot confidently act on.
// That matters because the bot also sits in group chats, where replying to
// every message would be noise — and because a guessed transaction writes
// silently to someone's ledger. Infrastructure errors are surfaced only when
// the message carried an image, which is a deliberate act; for plain prose
// they are logged and swallowed.
func (h *MessageHandler) handleNaturalLanguage(ctx context.Context, chatJID string, msg Message) Reply {
	deliberate := len(msg.Image) > 0

	sheetsClient, err := h.getSheetsClient(chatJID)
	if err != nil {
		// Unregistered chat: say nothing, exactly as before this feature.
		return Reply{}
	}

	wallets, err := sheetsClient.GetWallets(ctx)
	if err != nil {
		log.Printf("natural language: get wallets: %v", err)
		if deliberate {
			return Reply{Text: "❌ Gagal mengambil data dompet. Coba lagi."}
		}
		return Reply{}
	}

	names := make([]string, len(wallets))
	for i, w := range wallets {
		names[i] = w.Name
	}

	key := pendingKey(chatJID, msg.SenderID)
	draft, hasDraft := h.pending.get(key)

	var intent ai.Intent
	switch {
	case hasDraft && !deliberate && isCancel(msg.Text):
		h.pending.clear(key)
		return Reply{Text: cancelledDraftMsg}

	case hasDraft && !deliberate:
		// A bare answer ("flash", "pake gopay", "20rb") completes the draft
		// without a model round trip; anything richer falls through to the
		// model, which sees the draft and decides answer-or-new-request.
		if filled, ok := fillFromAnswer(draft, msg.Text, wallets); ok {
			intent = filled
			break
		}
		fallthrough

	default:
		req := ai.Request{
			Text:      msg.Text,
			Image:     msg.Image,
			ImageMIME: msg.ImageMIME,
			Wallets:   names,
			Now:       time.Now(),
		}
		if hasDraft && !deliberate {
			req.Pending = &draft
		}
		parseCtx, cancel := context.WithTimeout(ctx, parseTimeout)
		intent, err = h.parser.Parse(parseCtx, req)
		cancel()
		if err != nil {
			log.Printf("natural language: parse: %v", err)
			if deliberate {
				return Reply{Text: "❌ Gagal membaca pesan. Coba lagi atau pakai perintah manual (/help)."}
			}
			return Reply{}
		}

		// While a draft is open, "batal" means drop the draft — never undo
		// a transaction the user didn't just make.
		if hasDraft && !deliberate && intent.Action == ai.ActionUndo {
			h.pending.clear(key)
			return Reply{Text: cancelledDraftMsg}
		}
	}

	// With a single wallet there is nothing to disambiguate, so don't make the
	// user name it.
	if intent.Wallet == "" && len(names) == 1 && intent.Action != ai.ActionTransfer {
		intent.Wallet = names[0]
	}

	command, ok := intent.Command()
	if !ok {
		if intent.Mutates() {
			// A transaction we understood but can't record yet: remember it,
			// so the answer to the question below completes it.
			h.pending.set(key, intent)
			if reply := strings.TrimSpace(intent.Reply); reply != "" {
				return Reply{Text: "🤖 " + reply}
			}
			return Reply{Text: missingDetailMsg(intent, names)}
		}
		if reply := strings.TrimSpace(intent.Reply); reply != "" {
			return Reply{Text: "🤖 " + reply}
		}
		// Ordinary conversation — the model deliberately left Reply empty.
		// Any open draft stays open; it expires on its own.
		return Reply{}
	}

	// Whatever happens next, the conversation it belonged to is settled: a
	// completed draft is being recorded, or a new request replaced it.
	h.pending.clear(key)

	reply := h.handleCommand(ctx, chatJID, command)
	if reply.Text != "" {
		reply.Text += "\n\n_via: " + command + "_"
		if intent.Mutates() {
			reply.Text += "\n_Salah? Kirim */batal*_"
		}
	}
	return reply
}

// parseTimeout bounds one natural-language parse, retries included. A model
// call that hangs on a bad network otherwise holds the whole message timeout
// (60s), during which that chat's later messages wait in its queue. Giving up
// sooner lets the user fall back to a slash command quickly.
const parseTimeout = 20 * time.Second

// cancelledDraftMsg confirms a dropped draft, and points at /batal so nobody
// thinks it undid an earlier, already-recorded transaction.
const cancelledDraftMsg = "👌 Oke, tidak jadi dicatat.\n_Untuk membatalkan transaksi terakhir yang sudah tercatat, kirim */batal*_"

// missingDetailMsg explains what a recognised-but-incomplete request still
// needs, rather than guessing the rest.
func missingDetailMsg(intent ai.Intent, wallets []string) string {
	switch {
	case intent.Amount <= 0:
		return "🤖 Nominalnya berapa? Contoh: _beli bakso 20rb pakai gopay_"
	case len(wallets) == 0:
		return "🤖 Belum ada dompet. Tambah dulu dengan */dompet tambah <nama>*"
	case intent.Action == ai.ActionTransfer:
		return fmt.Sprintf("🤖 Transfer dari dompet mana ke mana?\nDompet kamu: %s", strings.Join(wallets, ", "))
	default:
		return fmt.Sprintf("🤖 Pakai dompet yang mana?\nDompet kamu: %s", strings.Join(wallets, ", "))
	}
}

// /daftar <spreadsheet_id>
func (h *MessageHandler) cmdDaftar(ctx context.Context, senderJID string, args []string) string {
	if len(args) == 0 {
		return "❌ Format: */daftar <spreadsheet_id>*\n\nContoh:\n/daftar 1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2upms\n\nID ada di URL Google Sheet kamu."
	}
	spreadsheetID := args[0]

	// Test access
	client, err := sheets.New(h.credentials, spreadsheetID)
	if err != nil {
		return "❌ Gagal terhubung ke Google Sheets. Pastikan ID spreadsheet benar."
	}
	if err := client.InitSheets(ctx); err != nil {
		return fmt.Sprintf("❌ Tidak bisa mengakses spreadsheet.\n\nPastikan kamu sudah share sheet ke:\n*keuanganku@keuanganku-504009.iam.gserviceaccount.com*\ndengan akses *Editor*.\n\nError: %v", err)
	}

	if err := h.users.Set(senderJID, userstore.User{SpreadsheetID: spreadsheetID}); err != nil {
		return "❌ Gagal menyimpan data. Coba lagi."
	}

	// Cache by spreadsheet ID so multiple chats sharing the same sheet reuse one client
	h.cacheMu.Lock()
	h.cache[spreadsheetID] = client
	h.cacheMu.Unlock()

	return "✅ *Berhasil terdaftar!*\n\nSpreadsheet kamu sudah terhubung. Ketik */help* untuk lihat perintah yang tersedia."
}

// /hapus — unregister
func (h *MessageHandler) cmdHapus(chatJID string) string {
	user, ok := h.users.Get(chatJID)
	if !ok {
		return "❌ Chat ini belum terdaftar."
	}
	h.users.Delete(chatJID)
	// Only remove from cache if no other chat uses the same spreadsheet
	if !h.users.AnyUses(user.SpreadsheetID) {
		h.cacheMu.Lock()
		delete(h.cache, user.SpreadsheetID)
		h.cacheMu.Unlock()
	}
	return "✅ Chat ini berhasil dihapus dari bot."
}

// /masuk <jumlah> <dompet> [kategori] [keterangan...]
// <dompet> may be multiple words (e.g. "Jago Kantong Belanja") — it's matched
// greedily against your registered wallets.
func (h *MessageHandler) cmdMasuk(ctx context.Context, sc *sheets.Client, args []string) string {
	if len(args) < 2 {
		return "❌ Format: */masuk <jumlah> <dompet> [kategori] [keterangan]*\nContoh: /masuk 5jt BCA Gaji Gaji bulan ini"
	}
	amount, err := parseAmount(args[0])
	if err != nil {
		return amountErrorMsg(args[0], err)
	}
	return addTransaction(ctx, sc, model.Income, amount, args[1:])
}

// /keluar <jumlah> <dompet> [kategori] [keterangan...]
func (h *MessageHandler) cmdKeluar(ctx context.Context, sc *sheets.Client, args []string) string {
	if len(args) < 2 {
		return "❌ Format: */keluar <jumlah> <dompet> [kategori] [keterangan]*\nContoh: /keluar 35rb GoPay Makanan Makan siang"
	}
	amount, err := parseAmount(args[0])
	if err != nil {
		return amountErrorMsg(args[0], err)
	}
	return addTransaction(ctx, sc, model.Expense, amount, args[1:])
}

// /dompet tambah <nama> | /dompet list
func (h *MessageHandler) cmdDompet(ctx context.Context, sc *sheets.Client, args []string) string {
	if len(args) == 0 {
		return "❌ Format: */dompet list* atau */dompet tambah <nama>*"
	}
	switch strings.ToLower(args[0]) {
	case "tambah", "add":
		if len(args) < 2 {
			return walletAddFormatMsg
		}
		name, opening, err := parseWalletAdd(args[1:])
		if err != nil {
			return fmt.Sprintf("❌ %v\n\n%s", err, walletAddFormatMsg)
		}
		if reply, handled := topUpExistingWallet(ctx, sc, name, opening); handled {
			return reply
		}
		if err := sc.AddWallet(ctx, name, opening); err != nil {
			return fmt.Sprintf("❌ %v", err)
		}
		msg := fmt.Sprintf("✅ Dompet *%s* berhasil ditambahkan!\n\n💰 Saldo awal: Rp %s", name, model.FormatAmount(opening))
		if opening == 0 {
			msg += "\n\n_Mau isi saldo awal? Lain kali bisa langsung: /dompet tambah " + name + " 500rb_"
		}
		return msg
	case "list", "daftar":
		wallets, err := sc.GetWallets(ctx)
		if err != nil {
			log.Printf("get wallets error: %v", err)
			return "❌ Gagal mengambil daftar dompet."
		}
		return listWallets(wallets)
	default:
		return "❌ Format: */dompet list* atau */dompet tambah <nama>*"
	}
}

// /transfer <jumlah> <dari> <ke> [keterangan]
func (h *MessageHandler) cmdTransfer(ctx context.Context, sc *sheets.Client, args []string) string {
	if len(args) < 3 {
		return "❌ Format: */transfer <jumlah> <dari> <ke> [keterangan]*\nContoh: /transfer 500rb BCA GoPay Uang jajan"
	}
	amount, err := parseAmount(args[0])
	if err != nil {
		return amountErrorMsg(args[0], err)
	}

	wallets, err := sc.GetWallets(ctx)
	if err != nil {
		return "❌ Gagal mengambil data dompet."
	}

	rest := args[1:]
	from, consumed := matchWalletPrefix(wallets, rest)
	if from == "" {
		return walletNotFoundMsg(wallets, rest[0])
	}
	rest = rest[consumed:]

	to, consumed := matchWalletPrefix(wallets, rest)
	if to == "" {
		attempted := ""
		if len(rest) > 0 {
			attempted = rest[0]
		}
		return walletNotFoundMsg(wallets, attempted)
	}
	rest = rest[consumed:]

	if strings.EqualFold(from, to) {
		return "❌ Dompet asal dan tujuan tidak boleh sama."
	}

	desc := strings.Join(rest, " ")
	if desc == "" {
		desc = "Transfer antar dompet"
	}

	now := time.Now()
	refID := uuid.New().String()
	outTx := model.Transaction{
		ID:          uuid.New().String(),
		Type:        model.Transfer,
		Direction:   model.DirectionOut,
		Amount:      amount,
		Wallet:      from,
		Category:    "Transfer",
		Description: fmt.Sprintf("Transfer ke %s: %s", to, desc),
		Date:        now,
		RefID:       refID,
	}
	if err := sc.AddTransaction(ctx, outTx); err != nil {
		if errors.Is(err, sheets.ErrInconsistent) {
			return fmt.Sprintf("❌ Transfer gagal dan spreadsheet perlu diperiksa manual. Error: %v", err)
		}
		return fmt.Sprintf("❌ Gagal memproses transfer: %v", err)
	}

	inTx := model.Transaction{
		ID:          uuid.New().String(),
		Type:        model.Transfer,
		Direction:   model.DirectionIn,
		Amount:      amount,
		Wallet:      to,
		Category:    "Transfer",
		Description: fmt.Sprintf("Transfer dari %s: %s", from, desc),
		Date:        now,
		RefID:       refID,
	}
	if err := sc.AddTransaction(ctx, inTx); err != nil {
		// Second leg failed — remove the first so the source wallet isn't
		// left short by a transfer that never arrived.
		if rbErr := sc.RemoveIfLast(ctx, outTx.ID); rbErr != nil {
			log.Printf("transfer rollback: %v (leg write: %v)", rbErr, err)
			return fmt.Sprintf("⚠️ Transfer gagal di tengah jalan: baris keluar dari *%s* sudah tercatat tapi baris masuk ke *%s* tidak. Tolong hapus atau batalkan baris itu di sheet (kolom *Canceled At*).", from, to)
		}
		log.Printf("transfer in-leg: %v", err)
		return fmt.Sprintf("❌ Gagal memproses transfer ke *%s*. Tidak ada yang tercatat, coba lagi.", to)
	}

	return fmt.Sprintf("🔄 *Transfer berhasil!*\n\n💸 Dari: %s\n💰 Ke: %s\n💵 Nominal: Rp %s\n📝 Keterangan: %s", from, to, model.FormatAmount(amount), desc)
}

func (h *MessageHandler) cmdSummary(ctx context.Context, sc *sheets.Client) string {
	summary, err := sc.GetSummary(ctx)
	if err != nil {
		return "❌ Gagal mengambil ringkasan."
	}
	return summary
}

// /rekap [bulanan|mingguan] [lalu] [pdf]
// /rekap bulan <nama_bulan> [tahun] [pdf]              contoh: /rekap bulan januari 2025 pdf
// /rekap minggu <tanggal> [nama_bulan] [tahun] [pdf]   contoh: /rekap minggu 3 januari 2026
func (h *MessageHandler) cmdRekap(ctx context.Context, sc *sheets.Client, args []string) Reply {
	wantPDF := false
	if n := len(args); n > 0 && strings.EqualFold(args[n-1], "pdf") {
		wantPDF = true
		args = args[:n-1]
	}

	now := time.Now()
	var from, to time.Time
	var title, errMsg string

	if len(args) == 0 {
		from, to = recap.MonthRange(now, 0)
		title = fmt.Sprintf("Rekap Bulanan — %s %d", recap.IndoMonthName(from.Month()), from.Year())
	} else {
		period := strings.ToLower(args[0])
		rest := args[1:]
		switch period {
		case "bulan", "bulanan", "month", "monthly":
			from, to, title, errMsg = resolveMonthRange(now, rest)
		case "minggu", "mingguan", "week", "weekly":
			from, to, title, errMsg = resolveWeekRange(now, rest)
		case "harian", "hari", "today", "day", "daily":
			from, to, title, errMsg = resolveDayRange(now, rest)
		case "kemarin", "yesterday":
			from, to, title, errMsg = resolveDayRange(now, []string{"lalu"})
		default:
			errMsg = rekapFormatHelp()
		}
	}

	if errMsg != "" {
		return Reply{Text: errMsg}
	}
	if wantPDF {
		return h.renderRekapPDF(ctx, sc, title, from, to)
	}
	return Reply{Text: h.renderRekap(ctx, sc, title, from, to)}
}

func (h *MessageHandler) renderRekap(ctx context.Context, sc *sheets.Client, title string, from, to time.Time) string {
	txs, err := sc.GetTransactionsInRange(ctx, from, to)
	if err != nil {
		return "❌ Gagal mengambil data transaksi."
	}
	return formatRecap(title, txs)
}

func (h *MessageHandler) renderRekapPDF(ctx context.Context, sc *sheets.Client, title string, from, to time.Time) Reply {
	txs, err := sc.GetTransactionsInRange(ctx, from, to)
	if err != nil {
		return Reply{Text: "❌ Gagal mengambil data transaksi."}
	}
	wallets, err := sc.GetWallets(ctx)
	if err != nil {
		return Reply{Text: "❌ Gagal mengambil data dompet."}
	}
	pdf, err := report.GenerateMonthly(title, txs, wallets)
	if err != nil {
		return Reply{Text: fmt.Sprintf("❌ Gagal membuat PDF: %v", err)}
	}
	filename := strings.ReplaceAll(strings.ReplaceAll(title, " ", "_"), "—", "-") + ".pdf"
	return Reply{Document: pdf, Filename: filename, Caption: title}
}

func rekapFormatHelp() string {
	return "❌ Format:\n" +
		"*/rekap* — bulan ini\n" +
		"*/rekap bulanan lalu* — bulan lalu\n" +
		"*/rekap bulan <nama_bulan> [tahun]* — contoh: /rekap bulan januari 2025\n" +
		"*/rekap mingguan* — minggu ini\n" +
		"*/rekap mingguan lalu* — minggu lalu\n" +
		"*/rekap minggu <tanggal> [nama_bulan] [tahun]* — contoh: /rekap minggu 3 januari 2026\n" +
		"*/rekap harian* — hari ini\n" +
		"*/rekap kemarin* — kemarin\n" +
		"*/rekap harian <tanggal> [nama_bulan] [tahun]* — contoh: /rekap harian 15 januari 2026\n" +
		"Tambahkan *pdf* di akhir untuk laporan PDF, contoh: /rekap bulan januari 2025 pdf"
}

func isPrevPeriodKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "lalu", "kemarin", "sebelumnya":
		return true
	}
	return false
}

// resolveMonthRange handles: (none) | lalu | <nama_bulan> [tahun]
func resolveMonthRange(now time.Time, rest []string) (from, to time.Time, title, errMsg string) {
	offset := 0
	switch {
	case len(rest) == 0:
		// current month
	case len(rest) == 1 && isPrevPeriodKeyword(rest[0]):
		offset = -1
	default:
		month, ok := recap.ParseIndoMonth(rest[0])
		if !ok {
			return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Nama bulan tidak dikenali: *%s*\nContoh: /rekap bulan januari 2025", rest[0])
		}
		year := now.Year()
		if len(rest) >= 2 {
			y, err := strconv.Atoi(rest[1])
			if err != nil || y < 2000 || y > 2100 {
				return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Tahun tidak valid: *%s*", rest[1])
			}
			year = y
		}
		first := time.Date(year, month, 1, 0, 0, 0, 0, now.Location())
		return first, first.AddDate(0, 1, 0), fmt.Sprintf("Rekap Bulanan — %s %d", recap.IndoMonthName(month), year), ""
	}

	from, to = recap.MonthRange(now, offset)
	title = fmt.Sprintf("Rekap Bulanan — %s %d", recap.IndoMonthName(from.Month()), from.Year())
	return from, to, title, ""
}

// resolveWeekRange handles: (none) | lalu | <tanggal> [nama_bulan] [tahun]
func resolveWeekRange(now time.Time, rest []string) (from, to time.Time, title, errMsg string) {
	offset := 0
	switch {
	case len(rest) == 0:
		// current week
	case len(rest) == 1 && isPrevPeriodKeyword(rest[0]):
		offset = -1
	default:
		day, err := strconv.Atoi(rest[0])
		if err != nil || day < 1 || day > 31 {
			return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Tanggal tidak valid: *%s*\nContoh: /rekap minggu 3 januari 2026", rest[0])
		}
		month := now.Month()
		year := now.Year()
		if len(rest) >= 2 {
			m, ok := recap.ParseIndoMonth(rest[1])
			if !ok {
				return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Nama bulan tidak dikenali: *%s*", rest[1])
			}
			month = m
		}
		if len(rest) >= 3 {
			y, err := strconv.Atoi(rest[2])
			if err != nil || y < 2000 || y > 2100 {
				return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Tahun tidak valid: *%s*", rest[2])
			}
			year = y
		}
		ref := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		from, to = recap.WeekRange(ref, 0)
		return from, to, fmt.Sprintf("Rekap Mingguan — %s", recap.FormatDateRange(from, to)), ""
	}

	from, to = recap.WeekRange(now, offset)
	title = fmt.Sprintf("Rekap Mingguan — %s", recap.FormatDateRange(from, to))
	return from, to, title, ""
}

// resolveDayRange handles: (none) | ini/sekarang | lalu | <tanggal> [nama_bulan] [tahun]
func resolveDayRange(now time.Time, rest []string) (from, to time.Time, title, errMsg string) {
	offset := 0
	switch {
	case len(rest) == 0:
		// today
	case len(rest) == 1 && isTodayKeyword(rest[0]):
		// today (explicit)
	case len(rest) == 1 && isPrevPeriodKeyword(rest[0]):
		offset = -1
	default:
		day, err := strconv.Atoi(rest[0])
		if err != nil || day < 1 || day > 31 {
			return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Tanggal tidak valid: *%s*\nContoh: /rekap harian 15 januari 2026", rest[0])
		}
		month := now.Month()
		year := now.Year()
		if len(rest) >= 2 {
			m, ok := recap.ParseIndoMonth(rest[1])
			if !ok {
				return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Nama bulan tidak dikenali: *%s*", rest[1])
			}
			month = m
		}
		if len(rest) >= 3 {
			y, err := strconv.Atoi(rest[2])
			if err != nil || y < 2000 || y > 2100 {
				return time.Time{}, time.Time{}, "", fmt.Sprintf("❌ Tahun tidak valid: *%s*", rest[2])
			}
			year = y
		}
		ref := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		from, to = recap.DayRange(ref, 0)
		return from, to, fmt.Sprintf("Rekap Harian — %s", recap.FormatDate(from)), ""
	}

	from, to = recap.DayRange(now, offset)
	title = fmt.Sprintf("Rekap Harian — %s", recap.FormatDate(from))
	return from, to, title, ""
}

func isTodayKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "ini", "sekarang":
		return true
	}
	return false
}

func formatRecap(title string, txs []model.Transaction) string {
	stats := recap.Compute(txs)

	msg := fmt.Sprintf("📊 *%s*\n\n", title)
	msg += fmt.Sprintf("💚 Total Pemasukan: Rp %s\n", model.FormatAmount(stats.TotalIncome))
	msg += fmt.Sprintf("🔴 Total Pengeluaran: Rp %s\n", model.FormatAmount(stats.TotalExpense))
	msg += fmt.Sprintf("💰 Selisih: Rp %s\n", model.FormatAmount(stats.TotalIncome-stats.TotalExpense))

	if len(stats.ExpenseByCategory) > 0 {
		msg += "\n*Rincian Pengeluaran:*\n" + formatCategoryBreakdown(stats.ExpenseByCategory)
	}
	if len(stats.IncomeByCategory) > 0 {
		msg += "\n*Rincian Pemasukan:*\n" + formatCategoryBreakdown(stats.IncomeByCategory)
	}
	if len(txs) == 0 {
		msg += "\n_Belum ada transaksi pada periode ini._"
	}

	return msg
}

func formatCategoryBreakdown(entries []recap.CategoryTotal) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("• %s: Rp %s\n", e.Category, model.FormatAmount(e.Total)))
	}
	return b.String()
}

// /batal — undo the most recent transaction that is still active. If it is one
// leg of a /transfer (linked via RefID), both legs are undone together.
//
// Undo is a soft delete: the row stays in the sheet with column J (Canceled
// At) filled in, and its effect is reversed out of the wallet balance. Because
// canceled rows are skipped, sending /batal again undoes the entry before it
// rather than hitting the same row twice.
func (h *MessageHandler) cmdBatal(ctx context.Context, sc *sheets.Client) string {
	entry, err := sc.LastActiveEntry(ctx)
	if err != nil {
		if errors.Is(err, sheets.ErrNoTransactions) {
			return "❌ Tidak ada lagi transaksi yang bisa dibatalkan."
		}
		log.Printf("batal: %v", err)
		return "❌ Gagal mengambil transaksi terakhir."
	}

	// Marking the row(s) canceled is the whole undo: balances are formulas
	// that skip canceled rows. Both legs of a transfer go in one API call, so
	// they are canceled together or not at all.
	rows := make([]int, len(entry))
	for i, e := range entry {
		rows[i] = e.Row
	}
	if err := sc.SetCanceled(ctx, rows, true, time.Now()); err != nil {
		log.Printf("batal: %v", err)
		return "❌ Gagal membatalkan transaksi. Tidak ada yang diubah, coba lagi."
	}

	return undoneMessage(entry)
}

// undoneMessage describes what /batal just canceled.
func undoneMessage(entry []sheets.TxRow) string {
	tx := entry[0].Tx
	when := tx.Date.Format("02/01/2006 15:04")
	footer := "\n\n_Baris tetap ada di sheet dengan tanda dibatalkan. Kirim */batal* lagi untuk membatalkan transaksi sebelumnya._"

	if tx.Type == model.Transfer && len(entry) == 2 {
		from, to := entry[1].Tx.Wallet, tx.Wallet
		if tx.Direction == model.DirectionOut {
			from, to = tx.Wallet, entry[1].Tx.Wallet
		}
		return fmt.Sprintf("↩️ *Transfer dibatalkan!*\n\n💸 Dari: %s\n💰 Ke: %s\n💵 Nominal: Rp %s\n🕐 Tanggal: %s",
			from, to, model.FormatAmount(tx.Amount), when) + footer
	}

	label := "💚 Pemasukan"
	if tx.Type == model.Expense {
		label = "🔴 Pengeluaran"
	} else if tx.Type == model.Transfer {
		label = "🔄 Transfer"
	}
	return fmt.Sprintf("↩️ *Transaksi dibatalkan!*\n\n%s: Rp %s\n💳 Dompet: %s\n🏷️ Kategori: %s\n📝 Keterangan: %s\n🕐 Tanggal: %s",
		label, model.FormatAmount(tx.Amount), tx.Wallet, tx.Category, tx.Description, when) + footer
}

func (h *MessageHandler) getSheetsClient(chatJID string) (*sheets.Client, error) {
	user, ok := h.users.Get(chatJID)
	if !ok {
		return nil, fmt.Errorf("not registered")
	}

	h.cacheMu.RLock()
	if c, ok := h.cache[user.SpreadsheetID]; ok {
		h.cacheMu.RUnlock()
		return c, nil
	}
	h.cacheMu.RUnlock()

	client, err := sheets.New(h.credentials, user.SpreadsheetID)
	if err != nil {
		return nil, err
	}

	h.cacheMu.Lock()
	h.cache[user.SpreadsheetID] = client
	h.cacheMu.Unlock()

	return client, nil
}

// matchWalletName resolves a case-insensitively typed wallet name to its
// canonical stored name, or "" if no wallet matches.
func matchWalletName(wallets []model.Wallet, name string) string {
	for _, w := range wallets {
		if strings.EqualFold(w.Name, name) {
			return w.Name
		}
	}
	return ""
}

// matchWalletPrefix greedily matches the longest leading run of tokens
// against a registered wallet name (case-insensitive), so multi-word wallet
// names like "Jago Kantong Belanja" work without quoting. Returns the
// canonical wallet name and how many tokens it consumed, or ("", 0) if no
// prefix matches any registered wallet.
func matchWalletPrefix(wallets []model.Wallet, tokens []string) (name string, consumed int) {
	for length := len(tokens); length >= 1; length-- {
		if m := matchWalletName(wallets, strings.Join(tokens[:length], " ")); m != "" {
			return m, length
		}
	}
	return "", 0
}

func walletNotFoundMsg(wallets []model.Wallet, attempted string) string {
	if len(wallets) == 0 {
		return "❌ Belum ada dompet. Tambah dulu dengan: */dompet tambah <nama>*"
	}
	names := make([]string, len(wallets))
	for i, w := range wallets {
		names[i] = w.Name
	}
	return fmt.Sprintf("❌ Dompet *%s* tidak ditemukan.\nDompet tersedia: %s", attempted, strings.Join(names, ", "))
}

// addTransaction resolves the (possibly multi-word) wallet name from the
// front of rest, treating whatever follows as [kategori] [keterangan...].
func addTransaction(ctx context.Context, sc *sheets.Client, txType model.TransactionType, amount float64, rest []string) string {
	wallets, err := sc.GetWallets(ctx)
	if err != nil {
		return "❌ Gagal mengambil data dompet."
	}

	matched, consumed := matchWalletPrefix(wallets, rest)
	if matched == "" {
		return walletNotFoundMsg(wallets, rest[0])
	}
	category, desc := extractCategoryAndDesc(rest[consumed:], "Lainnya")

	tx := model.Transaction{
		ID:          uuid.New().String(),
		Type:        txType,
		Amount:      amount,
		Wallet:      matched,
		Category:    category,
		Description: desc,
		Date:        time.Now(),
	}

	if err := sc.AddTransaction(ctx, tx); err != nil {
		if errors.Is(err, sheets.ErrInconsistent) {
			return fmt.Sprintf("❌ Transaksi gagal disimpan dan spreadsheet perlu diperiksa manual. Error: %v", err)
		}
		return fmt.Sprintf("❌ Gagal menyimpan transaksi: %v", err)
	}

	if txType == model.Income {
		return fmt.Sprintf("💚 *Pemasukan* dicatat!\n\n💳 Dompet: %s\n💰 Nominal: Rp %s\n🏷️ Kategori: %s\n📝 Keterangan: %s", matched, model.FormatAmount(amount), category, desc)
	}
	return fmt.Sprintf("🔴 *Pengeluaran* dicatat!\n\n💳 Dompet: %s\n💰 Nominal: Rp %s\n🏷️ Kategori: %s\n📝 Keterangan: %s", matched, model.FormatAmount(amount), category, desc)
}

// topUpExistingWallet handles "/dompet tambah <nama> <jumlah>" for a wallet
// that already exists: rather than refusing, it records the amount as an
// ordinary income (category Lainnya), which is visible in the history and
// undoable with /batal. It counts as income in /rekap — a deliberate choice
// over a separate adjustment type, for simplicity.
//
// handled is false when the wallet does not exist yet (the caller creates
// it), and true otherwise, with the reply to send.
func topUpExistingWallet(ctx context.Context, sc *sheets.Client, name string, amount float64) (reply string, handled bool) {
	wallets, err := sc.GetWallets(ctx)
	if err != nil {
		log.Printf("dompet tambah: %v", err)
		return "❌ Gagal mengambil data dompet.", true
	}
	existing := matchWalletName(wallets, name)
	if existing == "" {
		return "", false
	}

	switch {
	case amount == 0:
		return fmt.Sprintf("ℹ️ Dompet *%s* sudah ada.\n\nMau tambah saldonya? Kirim: */dompet tambah %s 500rb*", existing, existing), true
	case amount < 0:
		return fmt.Sprintf("❌ Dompet *%s* sudah ada. Untuk mengurangi saldo, catat sebagai pengeluaran: */keluar <jumlah> %s*", existing, existing), true
	}

	tokens := append(strings.Fields(existing), "Lainnya", "Tambah", "saldo")
	reply = addTransaction(ctx, sc, model.Income, amount, tokens)
	if strings.HasPrefix(reply, "❌") {
		return reply, true
	}
	return fmt.Sprintf("ℹ️ Dompet *%s* sudah ada, jadi saldonya ditambahkan sebagai pemasukan.\n\n%s\n\n_Salah? Kirim */batal*_", existing, reply), true
}

const walletAddFormatMsg ="Format: */dompet tambah <nama> [saldo awal]*\nContoh:\n• /dompet tambah BCA 2jt\n• /dompet tambah Bank Jago Kantong saldo 500rb\n• /dompet tambah GoPay  _(saldo 0)_"

// parseWalletAdd splits "/dompet tambah" arguments into a wallet name and an
// optional opening balance.
//
// The explicit form "<nama> saldo <jumlah>" is always unambiguous. The short
// form "<nama> <jumlah>" treats the last word as the balance only when it
// clearly reads as money (see looksLikeMoney), so a wallet called "GoPay 2"
// or "Tabungan 2025" keeps its full name.
func parseWalletAdd(tokens []string) (name string, opening float64, err error) {
	for i, t := range tokens {
		if strings.EqualFold(t, "saldo") && i > 0 {
			rest := tokens[i+1:]
			if len(rest) != 1 {
				return "", 0, fmt.Errorf("Tulis satu nominal setelah *saldo*.")
			}
			opening, err = parseOpening(rest[0])
			if err != nil {
				return "", 0, fmt.Errorf("Saldo awal *%s* tidak valid.", rest[0])
			}
			return strings.Join(tokens[:i], " "), opening, nil
		}
	}

	if n := len(tokens); n >= 2 && looksLikeMoney(tokens[n-1]) {
		if v, perr := parseOpening(tokens[n-1]); perr == nil {
			return strings.Join(tokens[:n-1], " "), v, nil
		}
	}
	return strings.Join(tokens, " "), 0, nil
}

// parseOpening reads an opening balance. Unlike a transaction amount it may
// be zero, or negative for a credit card or a debt ("-500rb").
func parseOpening(s string) (float64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.TrimPrefix(strings.TrimPrefix(t, "rp."), "rp")
	neg := strings.HasPrefix(t, "-")
	t = strings.TrimPrefix(t, "-")
	if t == "0" {
		return 0, nil
	}
	v, err := parseAmount(t)
	if err != nil {
		return 0, err
	}
	if neg {
		v = -v
	}
	return v, nil
}

// looksLikeMoney reports whether a trailing word in the short form is meant as
// a balance rather than part of the name: it carries a currency marker or
// shorthand suffix (rp, rb, ribu, k, jt, juta), or is a bare number of at
// least 10.000 — too large to be a wallet's ordinal or a year.
func looksLikeMoney(s string) bool {
	t := strings.ToLower(s)
	if strings.HasPrefix(t, "rp") {
		return true
	}
	for _, suf := range []string{"rb", "ribu", "k", "jt", "juta"} {
		if strings.HasSuffix(t, suf) && len(t) > len(suf) && unicode.IsDigit(rune(t[len(t)-len(suf)-1])) {
			return true
		}
	}
	v, err := strconv.ParseFloat(strings.TrimPrefix(t, "-"), 64)
	return err == nil && v >= 10000
}

func listWallets(wallets []model.Wallet) string {
	if len(wallets) == 0 {
		return "📭 Belum ada dompet. Tambah dengan: */dompet tambah <nama>*"
	}
	msg := "👛 *Daftar Dompet:*\n\n"
	for i, w := range wallets {
		msg += fmt.Sprintf("%d. *%s* — Rp %s\n", i+1, w.Name, model.FormatAmount(w.Balance))
	}
	return msg
}

func (h *MessageHandler) helpMessage() string {
	msg := `🤖 *Keuanganku Bot*

*Daftar perintah:*

📋 *Registrasi:*
/daftar <spreadsheet_id>  — hubungkan Google Sheet kamu
/hapus                    — hapus akun dari bot

💚 *Pemasukan:*
/masuk <jumlah> <dompet> [kategori] [keterangan]
_Contoh: /masuk 5jt BCA Gaji Gaji bulan ini_

🔴 *Pengeluaran:*
/keluar <jumlah> <dompet> [kategori] [keterangan]
_Contoh: /keluar 35rb GoPay Makanan Makan siang_

👛 *Dompet:*
/dompet tambah <nama> [saldo awal]  — tambah dompet baru
_Contoh: /dompet tambah BCA 2jt_
_Nama lebih dari satu kata: /dompet tambah Jago Kantong saldo 500rb_
/dompet list           — lihat semua dompet

🔄 *Transfer:*
/transfer <jumlah> <dari> <ke> [keterangan]
_Contoh: /transfer 500rb BCA GoPay Uang jajan_
_Nama dompet boleh lebih dari satu kata, contoh: /transfer 500rb Jago Kantong Belanja GoPay Beli barang_

📊 *Ringkasan:*
/summary  atau  /saldo

📈 *Rekap:*
/rekap                            — rekap bulan ini
/rekap harian                     — rekap hari ini
/rekap kemarin                    — rekap kemarin
/rekap mingguan                   — rekap minggu ini
/rekap bulanan lalu               — rekap bulan lalu
/rekap mingguan lalu              — rekap minggu lalu
/rekap bulan <nama_bulan> [tahun] — contoh: /rekap bulan januari 2025
/rekap minggu <tanggal> [bulan] [tahun] — contoh: /rekap minggu 3 januari 2026
/rekap harian <tanggal> [bulan] [tahun] — contoh: /rekap harian 15 januari 2026
Tambahkan *pdf* di akhir untuk laporan PDF, contoh: /rekap bulan lalu pdf

↩️ *Batal:*
/batal  — batalkan transaksi terakhir

*Format jumlah:* 50000 · 50rb · 1.5jt · 200k`

	// Only promise the scheduled report when the scheduler is actually
	// running — it is opt-in, and on some transports it costs money.
	if h.MonthlyReports {
		msg += "\n\n_Laporan PDF bulanan otomatis dikirim tiap tanggal 1 jam 07:00._"
	}
	return msg
}

// extractCategoryAndDesc treats the first word as category and the rest as description.
// If no args, returns defaultCategory and the category as description.
func extractCategoryAndDesc(args []string, defaultCategory string) (category, desc string) {
	if len(args) == 0 {
		return defaultCategory, defaultCategory
	}
	category = args[0]
	desc = strings.Join(args[1:], " ")
	if desc == "" {
		desc = category
	}
	return
}

func parseAmount(s string) (float64, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "juta"):
		s = strings.TrimSuffix(s, "juta")
		multiplier = 1_000_000
	case strings.HasSuffix(s, "jt"):
		s = strings.TrimSuffix(s, "jt")
		multiplier = 1_000_000
	case strings.HasSuffix(s, "ribu"):
		s = strings.TrimSuffix(s, "ribu")
		multiplier = 1_000
	case strings.HasSuffix(s, "rb"):
		s = strings.TrimSuffix(s, "rb")
		multiplier = 1_000
	case strings.HasSuffix(s, "k"):
		if len(s) > 1 && unicode.IsDigit(rune(s[len(s)-2])) {
			s = strings.TrimSuffix(s, "k")
			multiplier = 1_000
		}
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, errAmountInvalid
	}
	val *= multiplier

	// ParseFloat also accepts "NaN" and "Inf", and nothing downstream checks the
	// sign. A negative expense would invert its BalanceDelta and *raise* the
	// wallet balance, so reject anything that isn't a real positive amount.
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, errAmountInvalid
	}
	if val <= 0 {
		return 0, errAmountNotPositive
	}
	return val, nil
}

var (
	errAmountInvalid     = errors.New("invalid amount")
	errAmountNotPositive = errors.New("amount must be positive")
)

// amountErrorMsg renders a parseAmount failure as a user-facing message.
func amountErrorMsg(raw string, err error) string {
	if errors.Is(err, errAmountNotPositive) {
		return fmt.Sprintf("❌ Nominal harus lebih dari nol: *%s*", raw)
	}
	return fmt.Sprintf("❌ Nominal tidak valid: *%s*\nContoh: 50000, 50rb, 1.5jt, 200k", raw)
}
