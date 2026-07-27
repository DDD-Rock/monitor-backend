package notification

import (
	"database/sql"
	"testing"
	"time"
)

func TestStallNotificationDueRepeatsAtThreshold(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	threshold := 30 * time.Second
	if !stallNotificationDue(now.Add(-threshold), sql.NullTime{}, threshold, now) {
		t.Fatal("expected first stalled notification at threshold")
	}
	if stallNotificationDue(
		now.Add(-2*threshold),
		sql.NullTime{Time: now.Add(-10 * time.Second), Valid: true},
		threshold,
		now,
	) {
		t.Fatal("expected repeat notification to wait for another threshold")
	}
	if !stallNotificationDue(
		now.Add(-3*threshold),
		sql.NullTime{Time: now.Add(-threshold), Valid: true},
		threshold,
		now,
	) {
		t.Fatal("expected repeated stalled notification after another threshold")
	}
}

func TestNormalizeStallSeconds(t *testing.T) {
	t.Parallel()

	if value := normalizeStallSeconds(45); value != 45 {
		t.Fatalf("normalizeStallSeconds(45) = %d", value)
	}
	if value := normalizeStallSeconds(1); value != DefaultStallSeconds {
		t.Fatalf("expected out-of-range value to use default, got %d", value)
	}
}
