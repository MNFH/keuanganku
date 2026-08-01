// Package scheduler automatically sends a monthly PDF financial report to
// every registered chat, once a month.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/nurfaizh/keuanganku/internal/recap"
	"github.com/nurfaizh/keuanganku/internal/report"
	"github.com/nurfaizh/keuanganku/internal/sheets"
	"github.com/nurfaizh/keuanganku/internal/userstore"
	"github.com/nurfaizh/keuanganku/internal/wasend"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// fireDay and fireHour control when the monthly report is sent: the 1st of
// the month at 07:00 local time.
const (
	fireDay  = 1
	fireHour = 7
)

type Scheduler struct {
	users       *userstore.Store
	credentials string
	waClient    *whatsmeow.Client
}

func New(users *userstore.Store, credentials string, waClient *whatsmeow.Client) *Scheduler {
	return &Scheduler{users: users, credentials: credentials, waClient: waClient}
}

// Run blocks, sending the monthly report each time the schedule fires, until
// ctx is canceled.
func (s *Scheduler) Run(ctx context.Context) {
	for {
		next := nextFireTime(time.Now())
		log.Printf("Scheduler: next monthly report at %s", next.Format(time.RFC3339))
		timer := time.NewTimer(time.Until(next))

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.sendMonthlyReports(ctx)
		}
	}
}

func nextFireTime(now time.Time) time.Time {
	candidate := time.Date(now.Year(), now.Month(), fireDay, fireHour, 0, 0, 0, now.Location())
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 1, 0)
	}
	return candidate
}

func (s *Scheduler) sendMonthlyReports(ctx context.Context) {
	users, err := s.users.All()
	if err != nil {
		log.Printf("Scheduler: list users: %v", err)
		return
	}

	from, to := recap.MonthRange(time.Now(), -1) // previous full month
	title := fmt.Sprintf("Rekap Bulanan — %s %d", recap.IndoMonthName(from.Month()), from.Year())

	for jidStr, u := range users {
		if err := s.sendReportFor(ctx, jidStr, u.SpreadsheetID, title, from, to); err != nil {
			log.Printf("Scheduler: monthly report failed for %s: %v", jidStr, err)
		}
	}
}

func (s *Scheduler) sendReportFor(ctx context.Context, jidStr, spreadsheetID, title string, from, to time.Time) error {
	sc, err := sheets.New(s.credentials, spreadsheetID)
	if err != nil {
		return fmt.Errorf("connect sheet: %w", err)
	}

	txs, err := sc.GetTransactionsInRange(ctx, from, to)
	if err != nil {
		return fmt.Errorf("get transactions: %w", err)
	}
	wallets, err := sc.GetWallets(ctx)
	if err != nil {
		return fmt.Errorf("get wallets: %w", err)
	}

	pdf, err := report.GenerateMonthly(title, txs, wallets)
	if err != nil {
		return fmt.Errorf("generate pdf: %w", err)
	}

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return fmt.Errorf("parse jid: %w", err)
	}

	filename := fmt.Sprintf("Rekap-%s-%d.pdf", recap.IndoMonthName(from.Month()), from.Year())
	if err := wasend.SendDocument(ctx, s.waClient, jid, pdf, filename, title); err != nil {
		return fmt.Errorf("send document: %w", err)
	}

	log.Printf("Scheduler: sent monthly report to %s", jidStr)
	return nil
}
