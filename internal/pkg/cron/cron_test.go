package cron

import (
	"testing"
	"time"
)

func TestNextRunAfterIsAnchoredToWallClock(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	job := Job{Interval: 24 * time.Hour, Offset: time.Hour}

	cases := []struct{ now, want time.Time }{
		{time.Date(2026, 10, 5, 0, 30, 0, 0, loc), time.Date(2026, 10, 5, 1, 0, 0, 0, loc)},
		{time.Date(2026, 10, 5, 1, 0, 0, 0, loc), time.Date(2026, 10, 6, 1, 0, 0, 0, loc)},
		{time.Date(2026, 10, 5, 23, 59, 0, 0, loc), time.Date(2026, 10, 6, 1, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		if got := job.nextRunAfter(tc.now); !got.Equal(tc.want) {
			t.Fatalf("now %v: got %v, want %v", tc.now, got, tc.want)
		}
	}

	twice := Job{Interval: 12 * time.Hour, Offset: 2 * time.Hour}
	if got := twice.nextRunAfter(time.Date(2026, 10, 5, 3, 0, 0, 0, loc)); !got.Equal(time.Date(2026, 10, 5, 14, 0, 0, 0, loc)) {
		t.Fatalf("12h job: got %v", got)
	}
}
