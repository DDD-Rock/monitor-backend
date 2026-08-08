package notification

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAlertScanIntervalIsAtMostOneSecond(t *testing.T) {
	t.Parallel()

	if scheduleInterval > time.Second {
		t.Fatalf("告警扫描周期应不超过 1 秒，实际为 %s", scheduleInterval)
	}
}

func TestScheduledScansOfSameTypeDoNotOverlap(t *testing.T) {
	var service Service
	var running atomic.Bool
	started := make(chan struct{})
	release := make(chan struct{})

	if !service.startScheduledScan(context.Background(), &running, func(context.Context) {
		close(started)
		<-release
	}) {
		t.Fatal("首次扫描应当启动")
	}
	<-started

	if service.startScheduledScan(context.Background(), &running, func(context.Context) {}) {
		t.Fatal("同类型扫描尚未结束时不应重复启动")
	}
	close(release)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		secondStarted := make(chan struct{})
		if service.startScheduledScan(context.Background(), &running, func(context.Context) {
			close(secondStarted)
		}) {
			select {
			case <-secondStarted:
				return
			case <-time.After(time.Second):
				t.Fatal("前一次完成后，新扫描未执行")
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("前一次完成后，同类型扫描仍被锁定")
}
