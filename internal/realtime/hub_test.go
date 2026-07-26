package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"autobuff-monitor/server/internal/protocol"
)

func TestSubscriberReceivesSnapshotAndLatestFrame(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	detach := hub.AttachPublisher("session-1", "publisher-1")
	defer detach()

	channel, unsubscribe := hub.Subscribe("session-1", "viewer-1")
	defer unsubscribe()
	<-channel

	payload := json.RawMessage(`{"player":{"x":0.5,"y":0.4},"teammates":[],"others":[],"sourceFPS":30,"capturedAt":1}`)
	envelope := protocol.Envelope{Type: protocol.TypeFrame, Sequence: 1, Payload: payload}
	raw, _ := json.Marshal(envelope)
	hub.Publish("session-1", envelope, raw)

	select {
	case message := <-channel:
		var received protocol.Envelope
		if err := json.Unmarshal(message, &received); err != nil {
			t.Fatal(err)
		}
		if received.Type != protocol.TypeFrame {
			t.Fatalf("expected frame, got %s", received.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}
