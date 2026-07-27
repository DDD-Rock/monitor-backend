package notification

import (
	"testing"
	"time"

	"autobuff-monitor/server/internal/protocol"
)

func breachedZone(detectedAt time.Time) protocol.ZonePayload {
	return protocol.ZonePayload{
		Outside:    true,
		Rect:       &protocol.ZoneRect{X: 0.3, Y: 0.3, Width: 0.4, Height: 0.4},
		DetectedAt: detectedAt.UnixMilli(),
	}
}

func TestZoneBreachActiveRequiresOutsideAndFreshReport(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)

	if !zoneBreachActive(breachedZone(now.Add(-2*time.Second)), now) {
		t.Fatal("刚上报的越界状态应当有效")
	}

	inside := protocol.ZonePayload{
		Outside:    false,
		Rect:       &protocol.ZoneRect{X: 0.3, Y: 0.3, Width: 0.4, Height: 0.4},
		DetectedAt: now.UnixMilli(),
	}
	if zoneBreachActive(inside, now) {
		t.Fatal("回到安全区内不应继续报警")
	}
}

func TestZoneBreachActiveIgnoresStaleReports(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)

	// 客户端掉线后心跳停止，最后一次越界上报很快超出新鲜度窗口。
	stale := breachedZone(now.Add(-ZoneBreachFreshnessWindow - time.Second))
	if zoneBreachActive(stale, now) {
		t.Fatal("过期的越界上报不应再触发推送")
	}

	// 恰好落在窗口边界上仍然有效。
	if !zoneBreachActive(breachedZone(now.Add(-ZoneBreachFreshnessWindow)), now) {
		t.Fatal("边界上的上报应当仍然有效")
	}
}

func TestZoneBreachActiveRejectsMissingOrFutureTimestamps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)

	missing := breachedZone(now)
	missing.DetectedAt = 0
	if zoneBreachActive(missing, now) {
		t.Fatal("缺少时间戳应被拒绝")
	}

	future := breachedZone(now.Add(ZoneBreachFreshnessWindow + time.Minute))
	if zoneBreachActive(future, now) {
		t.Fatal("时钟明显超前的上报应被拒绝")
	}
}

func TestZoneBreachRepeatsEveryFiveSeconds(t *testing.T) {
	t.Parallel()

	if ZoneBreachIntervalSeconds != 5 {
		t.Fatalf("越界报警间隔应为 5 秒，实际 %d", ZoneBreachIntervalSeconds)
	}
	// 新鲜度窗口必须显著大于推送间隔，否则正常心跳抖动就会漏推。
	if ZoneBreachFreshnessWindow <= ZoneBreachIntervalSeconds*time.Second {
		t.Fatal("新鲜度窗口应明显大于推送间隔")
	}
	// 调度周期等于推送间隔，必须依赖容差，否则会顺延成 10 秒。
	if scheduleSlack <= 0 || scheduleSlack >= scheduleInterval {
		t.Fatalf("调度容差 %s 必须为正且小于扫描周期 %s", scheduleSlack, scheduleInterval)
	}
}
