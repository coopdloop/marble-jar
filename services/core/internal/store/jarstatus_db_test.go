package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/marble-jar/marble-jar/services/core/internal/store"
)

// The activity series is what the dashboard sparkline and streak badge read.
func TestJarStatus_ActivitySeries(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	mustCreateMarble(t, org, "one")
	mustCreateMarble(t, org, "two")

	status, err := testDB.JarStatus(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Daily) != store.JarStatusDays {
		t.Fatalf("daily has %d buckets, want %d", len(status.Daily), store.JarStatusDays)
	}
	first, last := status.Daily[0], status.Daily[store.JarStatusDays-1]
	if first.Day != utcDay(t, store.JarStatusDays-1) {
		t.Fatalf("oldest bucket = %s, want %s", first.Day, utcDay(t, store.JarStatusDays-1))
	}
	if last.Day != utcDay(t, 0) {
		t.Fatalf("newest bucket = %s, want today %s", last.Day, utcDay(t, 0))
	}
	if last.Marbles != 2 {
		t.Fatalf("today's bucket = %+v, want 2 marbles", last)
	}
	if last.CostUSD < 0.019 || last.CostUSD > 0.021 {
		t.Fatalf("today's cost = %v, want ~0.02", last.CostUSD)
	}
	for _, d := range status.Daily[:store.JarStatusDays-1] {
		if d.Marbles != 0 {
			t.Fatalf("bucket %s = %d marbles, want the zero-filled gap", d.Day, d.Marbles)
		}
	}
	if status.StreakDays != 1 {
		t.Fatalf("streak_days = %d, want 1", status.StreakDays)
	}
}

func TestJarStatus_StreakSpansDays(t *testing.T) {
	org := newOrg(t)
	ctx := context.Background()

	// Yesterday and the day before are active; today is still empty, which must
	// not break the streak, and a two-day gap before that caps it at 2.
	for _, ago := range []int{1, 2} {
		p := baseMarble(org, "backdated")
		p.OccurredAt = time.Now().UTC().AddDate(0, 0, -ago)
		mustCreateMarbleParams(t, p)
	}
	old := baseMarble(org, "long ago")
	old.OccurredAt = time.Now().UTC().AddDate(0, 0, -5)
	mustCreateMarbleParams(t, old)

	status, err := testDB.JarStatus(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if status.MarblesToday != 0 {
		t.Fatalf("marbles_today = %d, want 0", status.MarblesToday)
	}
	if status.StreakDays != 2 {
		t.Fatalf("streak_days = %d, want 2", status.StreakDays)
	}

	byDay := map[string]int{}
	for _, d := range status.Daily {
		byDay[d.Day] = d.Marbles
	}
	for _, ago := range []int{0, 1, 2, 3, 5} {
		key := utcDay(t, ago)
		want := map[int]int{0: 0, 1: 1, 2: 1, 3: 0, 5: 1}[ago]
		if byDay[key] != want {
			t.Fatalf("day %d ago (%s) = %d marbles, want %d", ago, key, byDay[key], want)
		}
	}
}

// utcDay renders the YYYY-MM-DD key for the day `ago` days before today.
func utcDay(t *testing.T, ago int) string {
	t.Helper()
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -ago)
	return day.Format(time.DateOnly)
}
