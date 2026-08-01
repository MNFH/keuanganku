package model

import (
	"fmt"
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

type ParsedIntent struct {
	Action      string          `json:"action"`       // add_income, add_expense, add_wallet, list_wallets, summary
	Amount      float64         `json:"amount"`
	Wallet      string          `json:"wallet"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	WalletName  string          `json:"wallet_name"` // for add_wallet action
}

// FormatAmount renders a rupiah amount with "." as the thousands separator, e.g. 500000 -> "500.000".
func FormatAmount(amount float64) string {
	s := fmt.Sprintf("%.0f", amount)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, '.')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
