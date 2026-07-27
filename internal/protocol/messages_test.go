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

func TestValidateEnvelopeAcceptsEXP(t *testing.T) {
	message := []byte(`{"type":"exp","sequence":2,"payload":{"currentEXP":123456,"percent":42.125,"confidence":0.96,"status":"已识别 EXP","recognizedAt":1}}`)
	if _, err := ValidateEnvelope(message); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnvelopeRejectsIncompleteEXP(t *testing.T) {
	message := []byte(`{"type":"exp","sequence":2,"payload":{"currentEXP":123456,"percent":null,"confidence":0.96,"status":"已识别 EXP","recognizedAt":1}}`)
	if _, err := ValidateEnvelope(message); err == nil {
		t.Fatal("expected incomplete EXP payload to be rejected")
	}
}

func TestValidateEnvelopeAcceptsRuneAlert(t *testing.T) {
	detected := []byte(`{"type":"rune","sequence":3,"payload":{"detected":true,"confidence":0.82,"detectedAt":1769000000000}}`)
	if _, err := ValidateEnvelope(detected); err != nil {
		t.Fatal(err)
	}

	cleared := []byte(`{"type":"rune","sequence":4,"payload":{"detected":false,"confidence":null,"detectedAt":1769000000000}}`)
	if _, err := ValidateEnvelope(cleared); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnvelopeRejectsMalformedRuneAlert(t *testing.T) {
	cases := map[string]string{
		"缺少时间戳":    `{"type":"rune","sequence":1,"payload":{"detected":true,"confidence":0.5,"detectedAt":0}}`,
		"置信度越界":    `{"type":"rune","sequence":1,"payload":{"detected":true,"confidence":1.4,"detectedAt":1769000000000}}`,
		"已解除却带置信度": `{"type":"rune","sequence":1,"payload":{"detected":false,"confidence":0.5,"detectedAt":1769000000000}}`,
	}
	for name, message := range cases {
		if _, err := ValidateEnvelope([]byte(message)); err == nil {
			t.Fatalf("expected %s 的符文消息被拒绝", name)
		}
	}
}
