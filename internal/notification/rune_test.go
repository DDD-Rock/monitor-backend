package notification

import (
	"testing"
	"time"

	"autobuff-monitor/server/internal/protocol"
)

func confidenceOf(value float64) *float64 {
	return &value
}

func TestRuneAlertActiveRequiresDetectedAndFreshReport(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 21, 0, 0, 0, time.UTC)

	if !runeAlertActive(protocol.RunePayload{
		Detected:   true,
		Confidence: confidenceOf(0.8),
		DetectedAt: now.Add(-2 * time.Second).UnixMilli(),
	}, now) {
		t.Fatal("expected a freshly detected rune to be active")
	}

	if runeAlertActive(protocol.RunePayload{
		Detected:   false,
		DetectedAt: now.UnixMilli(),
	}, now) {
		t.Fatal("expected a cleared rune to be inactive")
	}
}

func TestRuneAlertActiveIgnoresStaleReports(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 21, 0, 0, 0, time.UTC)

	// 客户端掉线后不再心跳，最后一次上报很快就会超出新鲜度窗口。
	stale := protocol.RunePayload{
		Detected:   true,
		Confidence: confidenceOf(0.9),
		DetectedAt: now.Add(-RuneAlertFreshnessWindow - time.Second).UnixMilli(),
	}
	if runeAlertActive(stale, now) {
		t.Fatal("expected a stale rune report to stop triggering pushes")
	}

	// 恰好落在窗口边界上仍然有效。
	edge := protocol.RunePayload{
		Detected:   true,
		Confidence: confidenceOf(0.9),
		DetectedAt: now.Add(-RuneAlertFreshnessWindow).UnixMilli(),
	}
	if !runeAlertActive(edge, now) {
		t.Fatal("expected a report at the freshness boundary to stay active")
	}
}

func TestRuneAlertActiveRejectsMissingOrFutureTimestamps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 21, 0, 0, 0, time.UTC)

	if runeAlertActive(protocol.RunePayload{Detected: true, DetectedAt: 0}, now) {
		t.Fatal("expected a missing timestamp to be rejected")
	}

	// 客户端时钟明显超前时同样不可信。
	future := protocol.RunePayload{
		Detected:   true,
		DetectedAt: now.Add(RuneAlertFreshnessWindow + time.Minute).UnixMilli(),
	}
	if runeAlertActive(future, now) {
		t.Fatal("expected a far-future timestamp to be rejected")
	}
}

func TestRuneAlertRepeatsEveryFiveSeconds(t *testing.T) {
	t.Parallel()

	if RuneAlertIntervalSeconds != 5 {
		t.Fatalf("符文推送间隔应为 5 秒，实际 %d", RuneAlertIntervalSeconds)
	}
	// 新鲜度窗口必须显著大于推送间隔，否则正常心跳抖动就会漏推。
	if RuneAlertFreshnessWindow <= RuneAlertIntervalSeconds*time.Second {
		t.Fatal("新鲜度窗口应明显大于推送间隔")
	}
	// 调度周期正好等于推送间隔，没有容差就会每次都差几毫秒而顺延成 10 秒。
	if scheduleInterval > RuneAlertIntervalSeconds*time.Second && scheduleSlack == 0 {
		t.Fatal("调度周期不小于推送间隔时必须留出容差")
	}
}

func TestScheduleSlackStaysBelowScanInterval(t *testing.T) {
	t.Parallel()

	if scheduleSlack <= 0 {
		t.Fatal("容差必须为正，否则每条规则都会顺延一个完整周期")
	}
	// 容差一旦达到扫描周期，同一轮里刚推过的规则会立刻再次入选。
	if scheduleSlack >= scheduleInterval {
		t.Fatalf("容差 %s 必须小于扫描周期 %s", scheduleSlack, scheduleInterval)
	}
}
