package store

import "testing"

// dailyStreak is the streak rule only; the series itself is covered by
// jarstatus_db_test.go, which runs against a real database.

// The streak is pure logic over a zero-filled series, so it is worth pinning
// down independently of the database.
func TestDailyStreak(t *testing.T) {
	cases := []struct {
		name   string
		counts []int
		want   int
	}{
		{"empty", nil, 0},
		{"all zero", []int{0, 0, 0}, 0},
		{"today only", []int{0, 0, 3}, 1},
		{"today still empty does not break it", []int{1, 2, 0}, 2},
		{"consecutive run", []int{0, 5, 1, 2}, 3},
		{"gap caps at the recent run", []int{1, 1, 0, 1, 1}, 2},
		{"whole window active", []int{1, 1, 1, 1}, 4},
		{"empty today with empty window", []int{0}, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dailyStreak(tc.counts); got != tc.want {
				t.Fatalf("dailyStreak(%v) = %d, want %d", tc.counts, got, tc.want)
			}
		})
	}
}
