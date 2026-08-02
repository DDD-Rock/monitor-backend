package notification

import (
	"database/sql"
	"testing"
	"time"

	"autobuff-monitor/server/internal/protocol"
)

func TestUrgentAlertVolumeOnlySoundsForFirstUnmutedDelivery(t *testing.T) {
	if got := urgentAlertVolume(true, false); got != 4 {
		t.Fatalf("first unmuted alert volume = %d, want 4", got)
	}
	if got := urgentAlertVolume(false, false); got != 0 {
		t.Fatalf("repeated alert volume = %d, want 0", got)
	}
	if got := urgentAlertVolume(true, true); got != 0 {
		t.Fatalf("muted first alert volume = %d, want 0", got)
	}
}

func TestAlertNotificationDueStartsNewCycleFromNullTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 2, 18, 0, 0, 0, time.UTC)
	if !alertNotificationDue(sql.NullTime{}, 5, now, 0) {
		t.Fatal("a reset cycle should send immediately")
	}
	if alertNotificationDue(sql.NullTime{Time: now.Add(-4 * time.Second), Valid: true}, 5, now, 0) {
		t.Fatal("repeat alert should wait for its full interval")
	}
	if !alertNotificationDue(sql.NullTime{Time: now.Add(-5 * time.Second), Valid: true}, 5, now, 0) {
		t.Fatal("repeat alert should send after its interval")
	}
}

func TestMouseFollowVerificationActiveRequiresFreshDetectedReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 17, 0, 0, 0, time.UTC)
	if !mouseFollowVerificationActive(protocol.VerificationPayload{
		Detected:   true,
		DetectedAt: now.UnixMilli(),
	}, now) {
		t.Fatal("fresh detected verification should trigger urgent notification")
	}
	if mouseFollowVerificationActive(protocol.VerificationPayload{
		Detected:   false,
		DetectedAt: now.UnixMilli(),
	}, now) {
		t.Fatal("cleared verification must not trigger notification")
	}
	if mouseFollowVerificationActive(protocol.VerificationPayload{
		Detected:   true,
		DetectedAt: now.Add(-MouseFollowVerificationFreshnessWindow - time.Second).UnixMilli(),
	}, now) {
		t.Fatal("stale verification must not trigger notification")
	}
}

func TestMouseFollowVerificationRepeatsEveryTwoSeconds(t *testing.T) {
	if MouseFollowVerificationIntervalSeconds != 2 {
		t.Fatalf(
			"mouse follow verification interval should be 2 seconds, got %d",
			MouseFollowVerificationIntervalSeconds,
		)
	}
	if MouseFollowVerificationFreshnessWindow <= MouseFollowVerificationIntervalSeconds*time.Second {
		t.Fatal("freshness window must cover at least one repeat interval")
	}
}
