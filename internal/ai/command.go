package ai

import (
	"fmt"
	"strconv"
	"strings"
)

// Command renders the intent as the canonical slash command the bot already
// implements. The bool reports whether the intent maps to a command at all —
// it is false for ActionUnknown, and for transaction intents that are missing
// something essential (no amount, no wallet), which the caller answers
// directly instead.
//
// Routing natural language back through the ordinary command path is what
// keeps the model honest: the generated command is re-validated by the same
// parseAmount, wallet matching and balance bookkeeping as a typed one, so a
// misread cannot bypass a rule that typed input has to satisfy.
func (in Intent) Command() (string, bool) {
	switch in.Action {
	case ActionExpense:
		return in.transactionCommand("/keluar")
	case ActionIncome:
		return in.transactionCommand("/masuk")

	case ActionTransfer:
		if in.Amount <= 0 || in.Wallet == "" || in.ToWallet == "" {
			return "", false
		}
		if strings.EqualFold(in.Wallet, in.ToWallet) {
			return "", false
		}
		cmd := fmt.Sprintf("/transfer %s %s %s", amountArg(in.Amount), in.Wallet, in.ToWallet)
		if desc := sanitizeLine(in.Description); desc != "" {
			cmd += " " + desc
		}
		return cmd, true

	case ActionAddWallet:
		name := sanitizeLine(in.Description)
		if name == "" {
			return "", false
		}
		return "/dompet tambah " + name, true

	case ActionBalance:
		return "/saldo", true

	case ActionRecap:
		cmd := "/rekap"
		if args := recapArgs(in.Period); args != "" {
			cmd += " " + args
		}
		if in.WantPDF {
			cmd += " pdf"
		}
		return cmd, true

	case ActionUndo:
		return "/batal", true

	case ActionHelp:
		return "/help", true

	default:
		return "", false
	}
}

// Mutates reports whether acting on this intent changes stored data, and so
// whether the reply should carry the "/batal to undo" hint.
func (in Intent) Mutates() bool {
	switch in.Action {
	case ActionExpense, ActionIncome, ActionTransfer:
		return true
	default:
		return false
	}
}

// transactionCommand builds "/masuk" and "/keluar", which share a shape:
// <cmd> <amount> <wallet> <category> <description...>
//
// Category is deliberately a single token: the handler reads the first word
// after the wallet as the category and the remainder as the description, so a
// multi-word category would silently swallow the description's first word.
func (in Intent) transactionCommand(cmd string) (string, bool) {
	if in.Amount <= 0 || in.Wallet == "" {
		return "", false
	}

	category := singleToken(in.Category)
	if category == "" {
		category = "Lainnya"
	}

	out := fmt.Sprintf("%s %s %s %s", cmd, amountArg(in.Amount), in.Wallet, category)
	if desc := sanitizeLine(in.Description); desc != "" {
		out += " " + desc
	}
	return out, true
}

// recapArgs maps a Period onto the arguments /rekap already understands.
func recapArgs(p Period) string {
	switch p {
	case PeriodLastMonth:
		return "bulanan lalu"
	case PeriodThisWeek:
		return "mingguan"
	case PeriodLastWeek:
		return "mingguan lalu"
	case PeriodToday:
		return "harian"
	case PeriodYesterday:
		return "kemarin"
	default:
		// PeriodNone and PeriodThisMonth: bare /rekap is already this month.
		return ""
	}
}

// amountArg renders an amount as a plain integer-ish string that parseAmount
// accepts. 'f' with precision -1 avoids scientific notation, which
// strconv.ParseFloat would accept but which reads as nonsense in an echoed
// command.
func amountArg(amount float64) string {
	return strconv.FormatFloat(amount, 'f', -1, 64)
}

// sanitizeLine collapses a value to a single clean line, since commands are
// parsed with strings.Fields and a newline would split one into two.
func sanitizeLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// singleToken reduces a value to its first whitespace-separated word.
func singleToken(s string) string {
	if fields := strings.Fields(s); len(fields) > 0 {
		return fields[0]
	}
	return ""
}
