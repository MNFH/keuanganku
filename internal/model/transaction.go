package model

import (
	"fmt"
	"strings"
	"time"
)

type TransactionType string

const (
	Income   TransactionType = "income"
	Expense  TransactionType = "expense"
	Transfer TransactionType = "transfer"
)

// Direction values only apply to Transfer transactions — they say which way
// money moved for that specific wallet leg, since Type alone can't (both
// legs of a transfer share Type == Transfer).
type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

type Transaction struct {
	ID          string          `json:"id"`
	Type        TransactionType `json:"type"`
	Amount      float64         `json:"amount"`
	Wallet      string          `json:"wallet"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Date        time.Time       `json:"date"`
	// RefID links the two legs of a /transfer together. Empty for regular
	// income/expense transactions.
	RefID string `json:"ref_id"`
	// Direction is set only when Type == Transfer (see Direction type above).
	Direction Direction `json:"direction"`
}

// BalanceDelta returns the signed effect this transaction has on tx.Wallet's
// balance (positive increases it, negative decreases it).
func (tx Transaction) BalanceDelta() float64 {
	switch tx.Type {
	case Income:
		return tx.Amount
	case Expense:
		return -tx.Amount
	case Transfer:
		if tx.Direction == DirectionOut {
			return -tx.Amount
		}
		return tx.Amount
	default:
		return 0
	}
}

type Wallet struct {
	Name    string  `json:"name"`
	Balance float64 `json:"balance"`
}

// FormatAmount renders a rupiah amount with "." as the thousands separator,
// e.g. 500000 -> "500.000" and -500000 -> "-500.000".
//
// The sign is stripped before grouping: counting it as a digit shifts every
// separator position, which rendered negatives as "-.500.000".
func FormatAmount(amount float64) string {
	s := fmt.Sprintf("%.0f", amount)

	sign := ""
	if digits, negative := strings.CutPrefix(s, "-"); negative {
		s = digits
		// An amount that rounds to zero shouldn't render as "-0".
		if strings.Trim(digits, "0") != "" {
			sign = "-"
		}
	}

	if len(s) <= 3 {
		return sign + s
	}

	var result []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, '.')
		}
		result = append(result, s[i])
	}
	return sign + string(result)
}
