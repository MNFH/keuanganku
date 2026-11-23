// Package scheduler automatically sends PDF financial reports — monthly,
// weekly, or both — to every registered chat.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/nurfaizh/keuanganku/internal/messaging"
	"github.com/nurfaizh/keuanganku/internal/recap"
	"github.com/nurfaizh/keuanganku/internal/report"
	"github.com/nurfaizh/keuanganku/internal/sheets"
	"github.com/nurfaizh/keuanganku/internal/userstore"
)

// fireHour is when reports go out: 07:00 local time, on the 1st of the month
// for the monthly report and on Monday for the weekly one.
const fireHour = 7

const (
	// checkInterval is how often the scheduler looks for a due report. It
	// polls rather than sleeping until the fire time because a single long
	// timer is lost to a restart and, on a laptop, paused by sleep — the
	// report would arrive late or not at all.
	checkInterval = 5 * time.Minute

	// retryInterval spaces out attempts for a chat whose delivery failed, so
	// a chat that keeps failing isn't retried every few minutes for days.
	retryInterval = time.Hour
)

// A Schedule is one kind of automatic report.
type Schedule struct {
	// Kind identifies the schedule in the delivery log ("monthly", "weekly").
	Kind string
	// latest returns the most recent fire time at or before now.
	latest func(now time.Time) time.Time
	// next returns the fire time after fire.
	next func(fire time.Time) time.Time
	// period returns the reporting window [from, to) a fire covers, and its
	// title.
	period func(fire time.Time) (from, to time.Time, title string)
	// grace is how long after its fire time a missed report is still sent.
	// Beyond it a report is stale, so a long outage doesn't end in a burst
	// of old reports.
	grace time.Duration
}

// Monthly fires on the 1st at 07:00 and reports the previous calendar month.
var Monthly = Schedule{
	Kind: "monthly",
	latest: func(now time.Time) time.Time {
		fire := time.Date(now.Year(), now.Month(), 1, fireHour, 0, 0, 0, now.Location())
		if fire.After(now) {
			fire = fire.AddDate(0, -1, 0)
		}
		return fire
	},
	next: func(fire time.Time) time.Time { return fire.AddDate(0, 1, 0) },
	period: func(fire time.Time) (time.Time, time.Time, string) {
		from, to := recap.MonthRange(fire, -1)
		return from, to, fmt.Sprintf("Rekap Bulanan — %s %d", recap.IndoMonthName(from.Month()), from.Year())
	},
	grace: 5 * 24 * time.Hour,
}

// Weekly fires on Monday at 07:00 and reports the previous Monday–Sunday week.
var Weekly = Schedule{
	Kind: "weekly",
	latest: func(now time.Time) time.Time {
		monday, _ := recap.WeekRange(now, 0)
		fire := monday.Add(fireHour * time.Hour)
		if fire.After(now) {
			fire = fire.AddDate(0, 0, -7)
		}
		return fire
	},
	next: func(fire time.Time) time.Time { return fire.AddDate(0, 0, 7) },
	period: func(fire time.Time) (time.Time, time.Time, string) {
		from, to := recap.WeekRange(fire, -1)
		return from, to, fmt.Sprintf("Rekap Mingguan — %s", recap.FormatDateRange(from, to))
	},
	grace: 2 * 24 * time.Hour,
}

// DeliveryLog records which reports each chat has received, so a report is
// sent once per chat per period even across restarts.
type DeliveryLog interface {
	Delivered(ctx context.Context, chatID, kind, period string) (bool, error)
	Record(ctx context.Context, chatID, kind, period string) error
}

// reportFunc fetches a chat's data for [from, to) and sends the PDF.
type reportFunc func(ctx context.Context, chatID, spreadsheetID, title, kind string, from, to time.Time) error

type Scheduler struct {
	users       *userstore.Store
	deliveries  DeliveryLog
	credentials string
	// sender routes each report to whichever chat platform the registered
	// chat belongs to, so Telegram users get their reports too.
	sender    messaging.Sender
	schedules []Schedule

	send reportFunc // s.sendReportFor; swapped out in tests

	// lastAttempt holds when a failed delivery was last tried, keyed by
	// deliveryKey. Only touched from Run's goroutine.
	lastAttempt map[string]time.Time
}

func New(users *userstore.Store, deliveries DeliveryLog, credentials string, sender messaging.Sender, schedules ...Schedule) *Scheduler {
	s := &Scheduler{
		users:       users,
		deliveries:  deliveries,
		credentials: credentials,
		sender:      sender,
		schedules:   schedules,
		lastAttempt: map[string]time.Time{},
	}
	s.send = s.sendReportFor
	return s
}

// Run blocks, sending each report once it is due, until ctx is canceled.
func (s *Scheduler) Run(ctx context.Context) {
	now := time.Now()
	for _, sch := range s.schedules {
		log.Printf("Scheduler: next %s report at %s", sch.Kind, sch.next(sch.latest(now)).Format(time.RFC3339))
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		users, err := s.users.All()
		if err != nil {
			log.Printf("Scheduler: list users: %v", err)
		} else {
			s.runDue(ctx, time.Now(), users)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runDue sends every report that is due at now and not yet delivered.
func (s *Scheduler) runDue(ctx context.Context, now time.Time, users map[string]userstore.User) {
	for _, sch := range s.schedules {
		fire := sch.latest(now)
		if now.Sub(fire) > sch.grace {
			continue
		}
		from, to, title := sch.period(fire)
		period := from.Format("2006-01-02")

		for chatID, u := range users {
			if ctx.Err() != nil {
				return
			}
			key := deliveryKey(chatID, sch.Kind, period)
			if last, ok := s.lastAttempt[key]; ok && now.Sub(last) < retryInterval {
				continue
			}
			done, err := s.deliveries.Delivered(ctx, chatID, sch.Kind, period)
			if err != nil {
				log.Printf("Scheduler: check %s delivery for %s: %v", sch.Kind, chatID, err)
				continue
			}
			if done {
				continue
			}

			if err := s.send(ctx, chatID, u.SpreadsheetID, title, sch.Kind, from, to); err != nil {
				s.lastAttempt[key] = now
				log.Printf("Scheduler: %s report failed for %s (retrying in %s): %v", sch.Kind, chatID, retryInterval, err)
				continue
			}
			delete(s.lastAttempt, key)
			// If this fails the report may be sent again next tick; a
			// duplicate is the better failure than a lost report.
			if err := s.deliveries.Record(ctx, chatID, sch.Kind, period); err != nil {
				log.Printf("Scheduler: record %s delivery for %s: %v", sch.Kind, chatID, err)
			}
		}
	}
}

func deliveryKey(chatID, kind, period string) string {
	return chatID + "|" + kind + "|" + period
}

func (s *Scheduler) sendReportFor(ctx context.Context, chatID, spreadsheetID, title, kind string, from, to time.Time) error {
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

	pdf, err := report.GenerateMonthly(title, from, to, txs, wallets)
	if err != nil {
		return fmt.Errorf("generate pdf: %w", err)
	}

	// Same naming as an on-demand /rekap ... pdf.
	filename := strings.ReplaceAll(strings.ReplaceAll(title, " ", "_"), "—", "-") + ".pdf"
	if err := s.sender.SendDocument(ctx, chatID, pdf, filename, title); err != nil {
		// Some transports refuse an unsolicited message. The WhatsApp Cloud
		// API only permits free-form content, documents included, within 24
		// hours of the user's own last message — and a report fired at 07:00
		// will usually fall outside that. The report cannot be pushed, so
		// prompt the user to make contact; once they do, /rekap delivers it
		// on demand.
		if errors.Is(err, messaging.ErrOutsideWindow) {
			re, ok := s.sender.(messaging.Reengager)
			if !ok {
				return fmt.Errorf("send document: %w", err)
			}
			if reErr := re.Reengage(ctx, chatID, kind+" report"); reErr != nil {
				return fmt.Errorf("send document: %w; re-engagement also failed: %v", err, reErr)
			}
			log.Printf("Scheduler: %s is outside the messaging window, sent a re-engagement prompt instead", chatID)
			return nil
		}
		return fmt.Errorf("send document: %w", err)
	}

	log.Printf("Scheduler: sent %s report to %s", kind, chatID)
	return nil
}
