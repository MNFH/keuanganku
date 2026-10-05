// Package scheduler automatically sends a monthly PDF financial report to
// every registered chat, once a month.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nurfaizh/keuanganku/internal/messaging"
	"github.com/nurfaizh/keuanganku/internal/recap"
	"github.com/nurfaizh/keuanganku/internal/report"
	"github.com/nurfaizh/keuanganku/internal/sheets"
	"github.com/nurfaizh/keuanganku/internal/userstore"
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
	// sender routes each report to whichever chat platform the registered
	// chat belongs to, so Telegram users get their monthly PDF too.
	sender messaging.Sender
}

func New(users *userstore.Store, credentials string, sender messaging.Sender) *Scheduler {
	return &Scheduler{users: users, credentials: credentials, sender: sender}
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

	filename := fmt.Sprintf("Rekap-%s-%d.pdf", recap.IndoMonthName(from.Month()), from.Year())
	if err := s.sender.SendDocument(ctx, jidStr, pdf, filename, title); err != nil {
		// Some transports refuse an unsolicited message. The WhatsApp Cloud
		// API only permits free-form content, documents included, within 24
		// hours of the user's own last message — and a monthly report fired
		// at 07:00 on the 1st will usually fall outside that. The report
		// cannot be pushed, so prompt the user to make contact; once they do,
		// /rekap delivers it on demand.
		if errors.Is(err, messaging.ErrOutsideWindow) {
			re, ok := s.sender.(messaging.Reengager)
			if !ok {
				return fmt.Errorf("send document: %w", err)
			}
			if reErr := re.Reengage(ctx, jidStr, "monthly report"); reErr != nil {
				return fmt.Errorf("send document: %w; re-engagement also failed: %v", err, reErr)
			}
			log.Printf("Scheduler: %s is outside the messaging window, sent a re-engagement prompt instead", jidStr)
			return nil
		}
		return fmt.Errorf("send document: %w", err)
	}

	log.Printf("Scheduler: sent monthly report to %s", jidStr)
	return nil
}
