package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nurfaizh/keuanganku/internal/userstore"
)

var jakarta = time.FixedZone("WIB", 7*3600)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, jakarta)
	if err != nil {
		panic(err)
	}
	return t
}

func TestScheduleArithmetic(t *testing.T) {
	tests := []struct {
		name       string
		sch        Schedule
		now        string
		fire, next string
		from, to   string // to is exclusive
		title      string
	}{
		{
			name: "weekly, Tuesday", sch: Weekly, now: "2026-11-17 08:00",
			fire: "2026-11-16 07:00", next: "2026-11-23 07:00",
			from: "2026-11-09 00:00", to: "2026-11-16 00:00",
			title: "Rekap Mingguan — 9–15 Nov 2026",
		},
		{
			name: "weekly, Monday before 07:00 is still last week's fire", sch: Weekly, now: "2026-11-16 06:59",
			fire: "2026-11-09 07:00", next: "2026-11-16 07:00",
			from: "2026-11-02 00:00", to: "2026-11-09 00:00",
			title: "Rekap Mingguan — 2–8 Nov 2026",
		},
		{
			name: "weekly, Sunday", sch: Weekly, now: "2026-11-22 23:00",
			fire: "2026-11-16 07:00", next: "2026-11-23 07:00",
			from: "2026-11-09 00:00", to: "2026-11-16 00:00",
			title: "Rekap Mingguan — 9–15 Nov 2026",
		},
		{
			name: "weekly, week spanning two months", sch: Weekly, now: "2026-12-07 07:00",
			fire: "2026-12-07 07:00", next: "2026-12-14 07:00",
			from: "2026-11-30 00:00", to: "2026-12-07 00:00",
			title: "Rekap Mingguan — 30 Nov – 6 Des 2026",
		},
		{
			name: "monthly, mid-month", sch: Monthly, now: "2026-11-17 08:00",
			fire: "2026-11-01 07:00", next: "2026-12-01 07:00",
			from: "2026-10-01 00:00", to: "2026-11-01 00:00",
			title: "Rekap Bulanan — Oktober 2026",
		},
		{
			name: "monthly, 1st before 07:00", sch: Monthly, now: "2026-12-01 06:00",
			fire: "2026-11-01 07:00", next: "2026-12-01 07:00",
			from: "2026-10-01 00:00", to: "2026-11-01 00:00",
			title: "Rekap Bulanan — Oktober 2026",
		},
		{
			name: "monthly, January reports December", sch: Monthly, now: "2027-01-01 07:00",
			fire: "2027-01-01 07:00", next: "2027-02-01 07:00",
			from: "2026-12-01 00:00", to: "2027-01-01 00:00",
			title: "Rekap Bulanan — Desember 2026",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fire := tt.sch.latest(at(tt.now))
			if !fire.Equal(at(tt.fire)) {
				t.Errorf("latest = %v, want %v", fire, tt.fire)
			}
			if next := tt.sch.next(fire); !next.Equal(at(tt.next)) {
				t.Errorf("next = %v, want %v", next, tt.next)
			}
			from, to, title := tt.sch.period(fire)
			if !from.Equal(at(tt.from)) || !to.Equal(at(tt.to)) {
				t.Errorf("period = [%v, %v), want [%v, %v)", from, to, tt.from, tt.to)
			}
			if title != tt.title {
				t.Errorf("title = %q, want %q", title, tt.title)
			}
		})
	}
}

type fakeLog map[string]bool

func (f fakeLog) Delivered(_ context.Context, chatID, kind, period string) (bool, error) {
	return f[deliveryKey(chatID, kind, period)], nil
}

func (f fakeLog) Record(_ context.Context, chatID, kind, period string) error {
	f[deliveryKey(chatID, kind, period)] = true
	return nil
}

type sent struct{ chatID, kind, title string }

func newTestScheduler(fail map[string]bool) (*Scheduler, fakeLog, *[]sent) {
	log := fakeLog{}
	var got []sent
	s := &Scheduler{
		deliveries:  log,
		schedules:   []Schedule{Monthly, Weekly},
		lastAttempt: map[string]time.Time{},
	}
	s.send = func(_ context.Context, chatID, _, title, kind string, _, _ time.Time) error {
		got = append(got, sent{chatID, kind, title})
		if fail[chatID] {
			return errors.New("send failed")
		}
		return nil
	}
	return s, log, &got
}

var twoChats = map[string]userstore.User{
	"tg:1": {SpreadsheetID: "a"},
	"tg:2": {SpreadsheetID: "b"},
}

func TestRunDueSendsOncePerChatPerPeriod(t *testing.T) {
	s, _, got := newTestScheduler(nil)
	ctx := context.Background()

	s.runDue(ctx, at("2026-11-16 07:00"), twoChats)
	if len(*got) != 2 {
		t.Fatalf("first tick sent %d reports, want 2 (weekly to each chat): %v", len(*got), *got)
	}
	for _, r := range *got {
		if r.kind != "weekly" {
			t.Errorf("sent %s report on a Monday mid-month, want only weekly", r.kind)
		}
	}

	// Later ticks — and a restart, since the log is what remembers — must
	// not send the same week again.
	s.runDue(ctx, at("2026-11-16 07:05"), twoChats)
	s.lastAttempt = map[string]time.Time{} // as after a restart
	s.runDue(ctx, at("2026-11-17 12:00"), twoChats)
	if len(*got) != 2 {
		t.Errorf("repeat ticks sent %d reports in total, want still 2: %v", len(*got), *got)
	}

	// The next Monday is a new period.
	s.runDue(ctx, at("2026-11-23 07:00"), twoChats)
	if len(*got) != 4 {
		t.Errorf("next week sent %d reports in total, want 4", len(*got))
	}
}

func TestRunDueCatchesUpAfterSleep(t *testing.T) {
	s, _, got := newTestScheduler(nil)

	// The laptop slept through Monday 07:00 and woke on Tuesday evening.
	s.runDue(context.Background(), at("2026-11-17 19:30"), twoChats)
	if len(*got) != 2 || (*got)[0].title != "Rekap Mingguan — 9–15 Nov 2026" {
		t.Errorf("after waking sent %v, want last week's report to both chats", *got)
	}
}

func TestRunDueSkipsStaleReports(t *testing.T) {
	s, _, got := newTestScheduler(nil)

	// Off from Sunday until Thursday: Monday's report is past its grace
	// period, so it is dropped rather than sent days late.
	s.runDue(context.Background(), at("2026-11-19 09:00"), twoChats)
	if len(*got) != 0 {
		t.Errorf("sent %v, want nothing once past the grace period", *got)
	}
}

func TestRunDueRetriesFailuresHourly(t *testing.T) {
	s, log, got := newTestScheduler(map[string]bool{"tg:2": true})
	ctx := context.Background()
	start := at("2026-11-16 07:00")

	s.runDue(ctx, start, twoChats)
	if !log[deliveryKey("tg:1", "weekly", "2026-11-09")] {
		t.Error("successful delivery to tg:1 was not recorded")
	}
	if log[deliveryKey("tg:2", "weekly", "2026-11-09")] {
		t.Error("failed delivery to tg:2 was recorded as delivered")
	}

	s.runDue(ctx, start.Add(30*time.Minute), twoChats)
	if len(*got) != 2 {
		t.Errorf("retried tg:2 after 30 minutes; attempts = %d, want 2", len(*got))
	}
	s.runDue(ctx, start.Add(61*time.Minute), twoChats)
	if len(*got) != 3 || (*got)[2].chatID != "tg:2" {
		t.Errorf("after an hour want one retry of tg:2, got %v", *got)
	}
}

func TestRunDueBothSchedulesOnTheFirst(t *testing.T) {
	s, _, got := newTestScheduler(nil)

	// 1 March 2027 is a Monday: both reports are due.
	s.runDue(context.Background(), at("2027-03-01 07:00"), map[string]userstore.User{"tg:1": {}})
	kinds := map[string]bool{}
	for _, r := range *got {
		kinds[r.kind] = true
	}
	if len(*got) != 2 || !kinds["monthly"] || !kinds["weekly"] {
		t.Errorf("sent %v, want one monthly and one weekly report", *got)
	}
}
