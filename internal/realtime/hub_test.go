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

func TestDeviceReceivesControlAndClientObserverIsNotified(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	updates, unsubscribe := hub.SubscribeClients(42, "observer-1")
	defer unsubscribe()
	<-updates

	controls, detach := hub.AttachDevice("session-1", 42, "publisher-1")
	defer detach()
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for device list update")
	}

	if !hub.SendCommand("session-1", protocol.ClientCommand{Type: "command", Action: "start"}) {
		t.Fatal("expected command to be accepted for online device")
	}
	select {
	case message := <-controls:
		if string(message) != `{"type":"command","action":"start"}` {
			t.Fatalf("unexpected command: %s", message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for command")
	}
}

func TestClientConnectionDoesNotMarkStoppedMonitorOnline(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, detach := hub.AttachDevice("session-1", 42, "publisher-1")
	defer detach()
	connected, _, _ := hub.ClientStatus("session-1")
	if !connected {
		t.Fatal("expected device to be connected")
	}
	_, monitorOnline, _ := hub.LatestEXP("session-1")
	if monitorOnline {
		t.Fatal("connected stopped device must not make monitor data online")
	}

	hub.Publish("session-1", protocol.Envelope{
		Type:    protocol.TypeClientState,
		Payload: json.RawMessage(`{"mode":"monitor","running":true}`),
	}, nil)
	_, monitorOnline, _ = hub.LatestEXP("session-1")
	if !monitorOnline {
		t.Fatal("running monitor mode should make monitor data online")
	}
}

func TestClientStateBroadcastsDerivedSnapshotAndTracksActiveMonitor(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, detach := hub.AttachDevice("session-1", 42, "publisher-1")
	channel, unsubscribe := hub.Subscribe("session-1", "viewer-1")
	defer unsubscribe()
	<-channel // Initial connected-but-stopped snapshot.

	active := protocol.Envelope{
		Type:    protocol.TypeClientState,
		Payload: json.RawMessage(`{"mode":"monitor","running":true}`),
	}
	if !hub.Publish("session-1", active, nil) {
		t.Fatal("expected monitor state to be accepted")
	}

	select {
	case message := <-channel:
		var snapshot protocol.Snapshot
		if err := json.Unmarshal(message, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Type != protocol.TypeSnapshot || !snapshot.Connected || !snapshot.Online {
			t.Fatalf("unexpected derived snapshot: %+v", snapshot)
		}
		if snapshot.ClientState == nil || snapshot.ClientState.Mode != "monitor" || !snapshot.ClientState.Running {
			t.Fatalf("missing active client state: %+v", snapshot.ClientState)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for derived client-state snapshot")
	}

	if sessionID, ok := hub.ActiveMonitorSession(42); !ok || sessionID != "session-1" {
		t.Fatalf("active monitor = %q, %v; want session-1, true", sessionID, ok)
	}
	detach()
	if sessionID, ok := hub.ActiveMonitorSession(42); ok || sessionID != "" {
		t.Fatalf("active monitor after detach = %q, %v; want empty, false", sessionID, ok)
	}
}

func TestOnlyOneDevicePerUserCanRunMonitorMode(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, detachFirst := hub.AttachDevice("session-1", 42, "publisher-1")
	defer detachFirst()
	_, detachSecond := hub.AttachDevice("session-2", 42, "publisher-2")
	defer detachSecond()

	active := protocol.Envelope{
		Type:    protocol.TypeClientState,
		Payload: json.RawMessage(`{"mode":"monitor","running":true}`),
	}
	if !hub.Publish("session-1", active, nil) {
		t.Fatal("expected the first monitor to be accepted")
	}
	if hub.Publish("session-2", active, nil) {
		t.Fatal("expected the second monitor on the same account to be rejected")
	}

	_, firstOnline, _ := hub.LatestEXP("session-1")
	_, secondOnline, _ := hub.LatestEXP("session-2")
	if !firstOnline || secondOnline {
		t.Fatalf("expected only the first monitor online, got first=%v second=%v", firstOnline, secondOnline)
	}

	stopped := protocol.Envelope{
		Type:    protocol.TypeClientState,
		Payload: json.RawMessage(`{"mode":"monitor","running":false}`),
	}
	if !hub.Publish("session-1", stopped, nil) {
		t.Fatal("expected stop state to be accepted")
	}
	if !hub.Publish("session-2", active, nil) {
		t.Fatal("expected the second monitor after the first stops to be accepted")
	}
}

func TestDifferentUsersCanRunMonitorModeAtTheSameTime(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, detachFirst := hub.AttachDevice("session-1", 42, "publisher-1")
	defer detachFirst()
	_, detachSecond := hub.AttachDevice("session-2", 43, "publisher-2")
	defer detachSecond()

	active := protocol.Envelope{
		Type:    protocol.TypeClientState,
		Payload: json.RawMessage(`{"mode":"monitor","running":true}`),
	}
	if !hub.Publish("session-1", active, nil) || !hub.Publish("session-2", active, nil) {
		t.Fatal("expected monitors belonging to different users to be accepted")
	}
}

func TestSimultaneousMonitorStartsHaveOneWinner(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	_, detachFirst := hub.AttachDevice("session-1", 42, "publisher-1")
	defer detachFirst()
	_, detachSecond := hub.AttachDevice("session-2", 42, "publisher-2")
	defer detachSecond()

	active := protocol.Envelope{
		Type:    protocol.TypeClientState,
		Payload: json.RawMessage(`{"mode":"monitor","running":true}`),
	}
	ready := make(chan struct{})
	results := make(chan bool, 2)
	for _, sessionID := range []string{"session-1", "session-2"} {
		go func() {
			<-ready
			results <- hub.Publish(sessionID, active, nil)
		}()
	}
	close(ready)

	accepted := 0
	for range 2 {
		if <-results {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted simultaneous monitor starts = %d, want 1", accepted)
	}
}

func TestDisconnectDeviceSignalsActiveConnection(t *testing.T) {
	t.Parallel()

	hub := NewHub()
	defer hub.Close()

	controls, detach := hub.AttachDevice("session-1", 42, "publisher-1")
	defer detach()

	if !hub.DisconnectDevice("session-1") {
		t.Fatal("expected active device to accept disconnect signal")
	}
	select {
	case command := <-controls:
		var decoded protocol.ClientCommand
		if err := json.Unmarshal(command, &decoded); err != nil {
			t.Fatalf("decode unbind command: %v", err)
		}
		if decoded.Action != "unbind" {
			t.Fatalf("expected unbind command, got %q", decoded.Action)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for unbind command")
	}
	select {
	case command := <-controls:
		if command != nil {
			t.Fatal("expected internal nil disconnect signal after unbind command")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect signal")
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

func TestLatestVerificationAndSnapshotTrackMostRecentReport(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	detach := hub.AttachPublisher("session-verification", "publisher-1")
	defer detach()

	payload := json.RawMessage(`{"detected":true,"confidence":0.93,"detectedAt":1769000000000}`)
	hub.Publish("session-verification", protocol.Envelope{
		Type:    protocol.TypeVerification,
		Payload: payload,
	}, nil)

	latest, online, ok := hub.LatestVerification("session-verification")
	if !ok || !online || !latest.Detected {
		t.Fatalf("expected active verification, got ok=%v online=%v payload=%+v", ok, online, latest)
	}

	channel, unsubscribe := hub.Subscribe("session-verification", "viewer-1")
	defer unsubscribe()
	select {
	case message := <-channel:
		var snapshot protocol.Snapshot
		if err := json.Unmarshal(message, &snapshot); err != nil {
			t.Fatal(err)
		}
		if string(snapshot.Verification) != string(payload) {
			t.Fatalf("expected verification payload %s, got %s", payload, snapshot.Verification)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for verification snapshot")
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

func TestPublishGainUpdatesSnapshotAndBroadcast(t *testing.T) {
	hub := NewHub()
	defer hub.Close()

	channel, unsubscribe := hub.Subscribe("session-1", "viewer-1")
	defer unsubscribe()
	<-channel

	gain := protocol.GainPayload{
		Inflow10m:  10,
		Outflow1h:  60,
		TotalUsage: 1000,
		DailyUsage: 200,
		SampledAt:  1769000000000,
	}
	if err := hub.PublishGain("session-1", gain); err != nil {
		t.Fatal(err)
	}

	select {
	case message := <-channel:
		var envelope protocol.Envelope
		if err := json.Unmarshal(message, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Type != protocol.TypeGain {
			t.Fatalf("expected gain, got %s", envelope.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for gain broadcast")
	}

	channel2, unsubscribe2 := hub.Subscribe("session-1", "viewer-2")
	defer unsubscribe2()
	select {
	case message := <-channel2:
		var snapshot protocol.Snapshot
		if err := json.Unmarshal(message, &snapshot); err != nil {
			t.Fatal(err)
		}
		var stored protocol.GainPayload
		if err := json.Unmarshal(snapshot.Gain, &stored); err != nil {
			t.Fatal(err)
		}
		if stored != gain {
			t.Fatalf("snapshot gain = %#v, want %#v", stored, gain)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for snapshot with gain")
	}
}
