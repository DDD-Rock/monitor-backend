package protocol

import "testing"

func TestValidateEnvelopeRejectsOutOfRangePoint(t *testing.T) {
	message := []byte(`{"type":"frame","sequence":1,"payload":{"player":{"x":1.2,"y":0.5},"teammates":[],"others":[],"sourceFPS":30,"capturedAt":1}}`)
	if _, err := ValidateEnvelope(message); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateEnvelopeAcceptsFrame(t *testing.T) {
	message := []byte(`{"type":"frame","sequence":1,"payload":{"player":{"x":0.2,"y":0.5},"teammates":[],"others":[],"sourceFPS":30,"capturedAt":1}}`)
	if _, err := ValidateEnvelope(message); err != nil {
		t.Fatal(err)
	}
}
