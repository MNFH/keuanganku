package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/nurfaizh/keuanganku/internal/model"
	"github.com/nurfaizh/keuanganku/internal/recap"
	"github.com/nurfaizh/keuanganku/internal/report"
	"github.com/nurfaizh/keuanganku/internal/sheets"
	"github.com/nurfaizh/keuanganku/internal/userstore"
)

type MessageHandler struct {
	users       *userstore.Store
	credentials string
	cache       map[string]*sheets.Client
	cacheMu     sync.RWMutex
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

func New(users *userstore.Store, credentials string) *MessageHandler {
	return &MessageHandler{
		users:       users,
		credentials: credentials,
		cache:       make(map[string]*sheets.Client),
	}
}

func (h *MessageHandler) Handle(ctx context.Context, senderJID, text string) Reply {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return Reply{}
	}

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
		return fmt.Sprintf("❌ Nominal tidak valid: *%s*\nContoh: 50000, 50rb, 1.5jt, 200k", args[0])
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
		return fmt.Sprintf("❌ Nominal tidak valid: *%s*\nContoh: 50000, 50rb, 1.5jt, 200k", args[0])
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
			return "❌ Format: */dompet tambah <nama>*\nContoh: /dompet tambah BCA"
		}
		name := strings.Join(args[1:], " ")
		if err := sc.AddWallet(ctx, name); err != nil {
			return fmt.Sprintf("❌ %v", err)
		}
		return fmt.Sprintf("✅ Dompet *%s* berhasil ditambahkan!", name)
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
		return fmt.Sprintf("❌ Nominal tidak valid: *%s*\nContoh: 50000, 50rb, 1.5jt, 200k", args[0])
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
		// Second leg failed — revert the first so the source wallet isn't left short.
		if rbErr := sc.AdjustWalletBalance(ctx, from, amount); rbErr != nil {
			return fmt.Sprintf("❌ Transfer gagal di tengah jalan dan saldo *%s* tidak bisa dikembalikan otomatis. Segera cek manual! Error: %v", from, rbErr)
		}
		if last, rowNum, lerr := sc.GetLastTransaction(ctx); lerr == nil && last.ID == outTx.ID {
			_ = sc.DeleteTransactionRow(ctx, rowNum)
		}
		return fmt.Sprintf("❌ Gagal memproses transfer ke *%s*, saldo *%s* sudah dikembalikan. Error: %v", to, from, err)
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

// /batal — undo the most recently recorded transaction. If the last entry is
// one leg of a /transfer (linked via RefID), both legs are undone together.
func (h *MessageHandler) cmdBatal(ctx context.Context, sc *sheets.Client) string {
	tx, rowNumber, err := sc.GetLastTransaction(ctx)
	if err != nil {
		if errors.Is(err, sheets.ErrNoTransactions) {
			return "❌ Belum ada transaksi untuk dibatalkan."
		}
		return "❌ Gagal mengambil transaksi terakhir."
	}

	if tx.RefID != "" {
		pairRow := rowNumber - 1
		if pair, perr := sc.GetTransactionRow(ctx, pairRow); perr == nil && pair.RefID == tx.RefID {
			return h.undoTransfer(ctx, sc, tx, rowNumber, pair, pairRow)
		}
	}

	return h.undoSingleTransaction(ctx, sc, tx, rowNumber)
}

func (h *MessageHandler) undoSingleTransaction(ctx context.Context, sc *sheets.Client, tx model.Transaction, rowNumber int) string {
	if err := sc.AdjustWalletBalance(ctx, tx.Wallet, -tx.BalanceDelta()); err != nil {
		return fmt.Sprintf("❌ Gagal membatalkan transaksi: %v", err)
	}
	if err := sc.DeleteTransactionRow(ctx, rowNumber); err != nil {
		return fmt.Sprintf("❌ Transaksi dibatalkan dari saldo, tapi gagal menghapus baris: %v", err)
	}

	label := "💚 Pemasukan"
	if tx.Type == model.Expense {
		label = "🔴 Pengeluaran"
	} else if tx.Type == model.Transfer {
		label = "🔄 Transfer"
	}
	return fmt.Sprintf("↩️ *Transaksi terakhir dibatalkan!*\n\n%s: Rp %s\n💳 Dompet: %s\n🏷️ Kategori: %s\n📝 Keterangan: %s", label, model.FormatAmount(tx.Amount), tx.Wallet, tx.Category, tx.Description)
}

func (h *MessageHandler) undoTransfer(ctx context.Context, sc *sheets.Client, last model.Transaction, lastRow int, pair model.Transaction, pairRow int) string {
	if err := sc.AdjustWalletBalance(ctx, last.Wallet, -last.BalanceDelta()); err != nil {
		return fmt.Sprintf("❌ Gagal membatalkan transfer: %v", err)
	}
	if err := sc.AdjustWalletBalance(ctx, pair.Wallet, -pair.BalanceDelta()); err != nil {
		return fmt.Sprintf("❌ Sebagian transfer dibatalkan (saldo *%s* sudah dikembalikan), tapi saldo *%s* gagal dikembalikan: %v. Segera cek manual!", last.Wallet, pair.Wallet, err)
	}

	// Delete the higher row first (lastRow > pairRow) so the lower row's index doesn't shift.
	if err := sc.DeleteTransactionRow(ctx, lastRow); err != nil {
		return fmt.Sprintf("❌ Saldo sudah dikembalikan, tapi gagal menghapus baris transfer: %v", err)
	}
	if err := sc.DeleteTransactionRow(ctx, pairRow); err != nil {
		return fmt.Sprintf("❌ Saldo sudah dikembalikan dan satu baris terhapus, tapi baris satunya gagal dihapus: %v", err)
	}

	from, to := pair.Wallet, last.Wallet
	if last.Direction == model.DirectionOut {
		from, to = last.Wallet, pair.Wallet
	}
	return fmt.Sprintf("↩️ *Transfer terakhir dibatalkan!*\n\n💸 Dari: %s\n💰 Ke: %s\n💵 Nominal: Rp %s", from, to, model.FormatAmount(last.Amount))
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
		return fmt.Sprintf("❌ Gagal menyimpan transaksi: %v", err)
	}

	if txType == model.Income {
		return fmt.Sprintf("💚 *Pemasukan* dicatat!\n\n💳 Dompet: %s\n💰 Nominal: Rp %s\n🏷️ Kategori: %s\n📝 Keterangan: %s", matched, model.FormatAmount(amount), category, desc)
	}
	return fmt.Sprintf("🔴 *Pengeluaran* dicatat!\n\n💳 Dompet: %s\n💰 Nominal: Rp %s\n🏷️ Kategori: %s\n📝 Keterangan: %s", matched, model.FormatAmount(amount), category, desc)
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
	return `🤖 *Keuanganku Bot*

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
/dompet tambah <nama>  — tambah dompet baru (nama boleh lebih dari satu kata)
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
_Laporan PDF bulanan juga otomatis dikirim tiap tanggal 1 jam 07:00._

↩️ *Batal:*
/batal  — batalkan transaksi terakhir

*Format jumlah:* 50000 · 50rb · 1.5jt · 200k`
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
		return 0, err
	}
	return val * multiplier, nil
}
