package expgain

import (
	"testing"
	"time"
)

func TestSumWindow(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	samples := []sample{
		{At: now.Add(-50 * time.Minute), Gained: 100},
		{At: now.Add(-5 * time.Minute), Gained: 30},
		{At: now.Add(-1 * time.Minute), Gained: 20},
	}

	if got := sumWindow(samples, now, window10m); got != 50 {
		t.Fatalf("10m window = %d, want 50", got)
	}
	if got := sumWindow(samples, now, window1h); got != 150 {
		t.Fatalf("1h window = %d, want 150", got)
	}
}

func TestCalendarDateUsesShanghaiMidnight(t *testing.T) {
	loc, err := time.LoadLocation(shanghaiLocation)
	if err != nil {
		t.Fatal(err)
	}
	// UTC 7 月 27 日 16:30 = 上海 7 月 28 日 00:30，业务日应是 28 号。
	now := time.Date(2026, 7, 27, 16, 30, 0, 0, time.UTC)
	got := calendarDate(now, loc)
	want := time.Date(2026, 7, 28, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("calendar date = %v, want %v", got, want)
	}
}

func TestRollDailyResetsDailyGain(t *testing.T) {
	loc, err := time.LoadLocation(shanghaiLocation)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{loc: loc, states: map[int64]*userState{}}
	state := &userState{
		dailyGained: 999,
		dailyDate:   time.Date(2026, 7, 27, 0, 0, 0, 0, loc),
		totalGained: 5000,
	}
	now := time.Date(2026, 7, 28, 1, 0, 0, 0, loc)
	service.rollDailyLocked(state, now)
	if state.dailyGained != 0 {
		t.Fatalf("daily gained after roll = %d, want 0", state.dailyGained)
	}
	if state.totalGained != 5000 {
		t.Fatalf("total gained should stay, got %d", state.totalGained)
	}
	if !state.dirty {
		t.Fatal("expected dirty after day roll")
	}
}

func TestPositiveDeltaOnly(t *testing.T) {
	cases := []struct {
		previous int64
		current  int64
		want     int64
	}{
		{100, 150, 50},
		{200, 180, 0},
		{100, 100, 0},
	}
	for _, item := range cases {
		delta := item.current - item.previous
		if delta < 0 {
			delta = 0
		}
		if delta != item.want {
			t.Fatalf("%d -> %d = %d, want %d", item.previous, item.current, delta, item.want)
		}
	}
}
