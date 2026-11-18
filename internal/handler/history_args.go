package handler

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/model"
)

const (
	defaultHistoryCount = 10 // Default_Count
	maxHistoryCount     = 30 // Max_Count
)

// historyArgs is /riwayat's parsed arguments.
type historyArgs struct {
	Count     int     // entries to show, 1..maxHistoryCount
	Requested int     // what the user asked for; > maxHistoryCount means capped
	Period    *period // nil = no period
	Wallet    string  // canonical wallet name, "" = every wallet
}

// numberShaped matches anything a user might mean as a count, including the
// ones we reject (negative, decimal), so they get the format reply instead
// of being taken for a wallet name.
var numberShaped = regexp.MustCompile(`^[+-]?\d+([.,]\d+)?$`)

// parseHistoryArgs reads /riwayat [jumlah] [periode] [dompet].
//
// The order is fixed and nothing is guessed: a whole-text wallet match wins,
// then a leading number that starts a valid period is that period ("5
// januari"), otherwise it is the count; then an optional period; then a
// wallet. When the arguments cannot be read it returns a reply to send
// instead: the format help, or the wallet-not-found message.
func parseHistoryArgs(now time.Time, wallets []model.Wallet, args []string) (historyArgs, string) {
	out := historyArgs{Count: defaultHistoryCount, Requested: defaultHistoryCount}
	if len(args) == 0 {
		return out, ""
	}
	if name := matchWalletName(wallets, strings.Join(args, " ")); name != "" {
		out.Wallet = name
		return out, ""
	}

	i := 0
	if numberShaped.MatchString(args[0]) {
		p, used, errMsg := parsePeriod(now, args)
		switch {
		case errMsg != "":
			return out, errMsg + "\n\n" + historyFormatHelp()
		case p != nil:
			out.Period, i = p, used
		default:
			n, ok := parseCount(args[0])
			if !ok {
				return out, historyFormatHelp()
			}
			out.Requested, out.Count = n, min(n, maxHistoryCount)
			i = 1
		}
	}

	if out.Period == nil && i < len(args) {
		p, used, errMsg := parsePeriod(now, args[i:])
		if errMsg != "" {
			return out, errMsg + "\n\n" + historyFormatHelp()
		}
		if p != nil {
			out.Period, i = p, i+used
		}
	}

	rest := args[i:]
	if len(rest) == 0 {
		return out, ""
	}
	name, used := matchWalletPrefix(wallets, rest)
	if name == "" {
		// A count or period after unknown text is a wrong order, not a
		// wallet name.
		if outOfOrder(now, rest[1:]) {
			return out, historyFormatHelp()
		}
		return out, walletNotFoundMsg(wallets, strings.Join(rest, " "))
	}
	if used < len(rest) {
		return out, historyFormatHelp()
	}
	out.Wallet = name
	return out, ""
}

// parseCount accepts a whole number >= 1. Huge numbers are simply capped
// later, so they parse as "more than the maximum".
func parseCount(s string) (int, bool) {
	if !isDigits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return maxHistoryCount + 1, true // too long for an int: far above the cap
	}
	return n, n >= 1
}

// outOfOrder reports whether tokens contain a count or a period, i.e. the
// user put them after the wallet.
func outOfOrder(now time.Time, tokens []string) bool {
	for i, tok := range tokens {
		if numberShaped.MatchString(tok) {
			return true
		}
		if p, _, errMsg := parsePeriod(now, tokens[i:]); p != nil || errMsg != "" {
			return true
		}
	}
	return false
}

// historyHelp is the /riwayat block shared by /help and the format reply.
const historyHelp = `📜 *Riwayat:*
/riwayat [jumlah] [periode] [dompet]  — lihat transaksi terakhir (alias /history)
_Contoh: /riwayat · /riwayat 20 · /riwayat mingguan GoPay · /riwayat 5 januari_
_Periode sama seperti /rekap. Jumlah maksimal 30._`

func historyFormatHelp() string {
	return "❌ Format: */riwayat [jumlah] [periode] [dompet]*\n\n" + historyHelp
}
