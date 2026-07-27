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

func TestSnapshotIncludesLatestEXP(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	payload := json.RawMessage(`{"currentEXP":123456,"percent":42.125,"confidence":0.96,"status":"已识别 EXP","recognizedAt":1}`)
	envelope := protocol.Envelope{Type: protocol.TypeEXP, Sequence: 1, Payload: payload}
	hub.Publish("session-1", envelope, nil)

	channel, unsubscribe := hub.Subscribe("session-1", "viewer-1")
	defer unsubscribe()

	select {
	case message := <-channel:
		var snapshot protocol.Snapshot
		if err := json.Unmarshal(message, &snapshot); err != nil {
			t.Fatal(err)
		}
		if string(snapshot.EXP) != string(payload) {
			t.Fatalf("expected EXP payload %s, got %s", payload, snapshot.EXP)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestLatestRuneTracksMostRecentReport(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	detach := hub.AttachPublisher("session-1", "publisher-1")
	defer detach()

	publishRune := func(body string) {
		payload := json.RawMessage(body)
		hub.Publish("session-1", protocol.Envelope{
			Type:    protocol.TypeRune,
			Payload: payload,
		}, nil)
	}

	publishRune(`{"detected":true,"confidence":0.8,"detectedAt":1769000000000}`)
	payload, online, ok := hub.LatestRune("session-1")
	if !ok || !online || !payload.Detected {
		t.Fatalf("expected an online detected rune, got ok=%v online=%v payload=%+v", ok, online, payload)
	}

	publishRune(`{"detected":false,"confidence":null,"detectedAt":1769000005000}`)
	payload, _, ok = hub.LatestRune("session-1")
	if !ok || payload.Detected {
		t.Fatalf("expected the cleared rune state to replace the detected one, got %+v", payload)
	}
}

func TestLatestRuneReportsOfflineAfterPublisherDetaches(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	detach := hub.AttachPublisher("session-1", "publisher-1")
	hub.Publish("session-1", protocol.Envelope{
		Type:    protocol.TypeRune,
		Payload: json.RawMessage(`{"detected":true,"confidence":0.8,"detectedAt":1769000000000}`),
	}, nil)
	detach()

	payload, online, ok := hub.LatestRune("session-1")
	if !ok {
		t.Fatal("expected the last rune report to remain readable")
	}
	if online {
		t.Fatal("expected the room to be offline once the publisher detaches")
	}
	if !payload.Detected {
		t.Fatal("expected the stored payload to stay unchanged")
	}
}

func TestLatestRuneReturnsNothingForUnknownSession(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	if _, _, ok := hub.LatestRune("missing"); ok {
		t.Fatal("expected no rune data for an unknown session")
	}
}

func TestSnapshotIncludesLatestRune(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	payload := json.RawMessage(`{"detected":true,"confidence":0.82,"detectedAt":1769000000000}`)
	hub.Publish("session-1", protocol.Envelope{Type: protocol.TypeRune, Payload: payload}, nil)

	channel, unsubscribe := hub.Subscribe("session-1", "viewer-1")
	defer unsubscribe()

	select {
	case message := <-channel:
		var snapshot protocol.Snapshot
		if err := json.Unmarshal(message, &snapshot); err != nil {
			t.Fatal(err)
		}
		if string(snapshot.Rune) != string(payload) {
			t.Fatalf("expected rune payload %s, got %s", payload, snapshot.Rune)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}
}

func TestLatestZoneTracksMostRecentReport(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	detach := hub.AttachPublisher("session-1", "publisher-1")
	defer detach()

	publishZone := func(body string) {
		hub.Publish("session-1", protocol.Envelope{
			Type:    protocol.TypeZone,
			Payload: json.RawMessage(body),
		}, nil)
	}

	publishZone(`{"outside":true,"rect":{"x":0.3,"y":0.3,"width":0.4,"height":0.4},"detectedAt":1769000000000}`)
	payload, online, ok := hub.LatestZone("session-1")
	if !ok || !online || !payload.Outside {
		t.Fatalf("expected an online zone breach, got ok=%v online=%v payload=%+v", ok, online, payload)
	}
	if payload.Rect == nil || payload.Rect.Width != 0.4 {
		t.Fatalf("expected the rect to survive the round trip, got %+v", payload.Rect)
	}

	publishZone(`{"outside":false,"rect":{"x":0.3,"y":0.3,"width":0.4,"height":0.4},"detectedAt":1769000005000}`)
	payload, _, ok = hub.LatestZone("session-1")
	if !ok || payload.Outside {
		t.Fatalf("expected the back-inside state to replace the breach, got %+v", payload)
	}
}

func TestLatestZoneReportsOfflineAfterPublisherDetaches(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	detach := hub.AttachPublisher("session-1", "publisher-1")
	hub.Publish("session-1", protocol.Envelope{
		Type:    protocol.TypeZone,
		Payload: json.RawMessage(`{"outside":true,"rect":{"x":0.3,"y":0.3,"width":0.4,"height":0.4},"detectedAt":1769000000000}`),
	}, nil)
	detach()

	payload, online, ok := hub.LatestZone("session-1")
	if !ok {
		t.Fatal("expected the last zone report to remain readable")
	}
	if online {
		t.Fatal("expected the room to be offline once the publisher detaches")
	}
	if !payload.Outside {
		t.Fatal("expected the stored payload to stay unchanged")
	}
}

func TestLatestZoneReturnsNothingForUnknownSession(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	if _, _, ok := hub.LatestZone("missing"); ok {
		t.Fatal("expected no zone data for an unknown session")
	}
}

func TestSnapshotIncludesLatestZone(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	payload := json.RawMessage(`{"outside":true,"rect":{"x":0.1,"y":0.2,"width":0.5,"height":0.5},"detectedAt":1769000000000}`)
	hub.Publish("session-1", protocol.Envelope{Type: protocol.TypeZone, Payload: payload}, nil)

	channel, unsubscribe := hub.Subscribe("session-1", "viewer-1")
	defer unsubscribe()

	select {
	case message := <-channel:
		var snapshot protocol.Snapshot
		if err := json.Unmarshal(message, &snapshot); err != nil {
			t.Fatal(err)
		}
		if string(snapshot.Zone) != string(payload) {
			t.Fatalf("expected zone payload %s, got %s", payload, snapshot.Zone)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot")
	}
}
