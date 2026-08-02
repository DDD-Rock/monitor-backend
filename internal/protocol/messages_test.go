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
	message := []byte(`{"type":"exp","sequence":2,"payload":{"currentEXP":123456,"percent":42.125,"confidence":0.96,"recognitionMethod":"ppOCRv4","status":"已识别 EXP","recognizedAt":1}}`)
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

func TestValidateEnvelopeAcceptsMouseFollowVerification(t *testing.T) {
	detected := []byte(`{"type":"verification","sequence":5,"payload":{"detected":true,"confidence":0.91,"detectedAt":1769000000000}}`)
	if _, err := ValidateEnvelope(detected); err != nil {
		t.Fatal(err)
	}

	cleared := []byte(`{"type":"verification","sequence":6,"payload":{"detected":false,"confidence":null,"detectedAt":1769000005000}}`)
	if _, err := ValidateEnvelope(cleared); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnvelopeRejectsMalformedMouseFollowVerification(t *testing.T) {
	cases := map[string]string{
		"缺少时间戳":    `{"type":"verification","sequence":1,"payload":{"detected":true,"confidence":0.8,"detectedAt":0}}`,
		"置信度越界":    `{"type":"verification","sequence":1,"payload":{"detected":true,"confidence":1.2,"detectedAt":1769000000000}}`,
		"已解除却带置信度": `{"type":"verification","sequence":1,"payload":{"detected":false,"confidence":0.8,"detectedAt":1769000000000}}`,
	}
	for name, message := range cases {
		if _, err := ValidateEnvelope([]byte(message)); err == nil {
			t.Fatalf("expected %s 的鼠标跟随验证消息被拒绝", name)
		}
	}
}

func TestValidateEnvelopeAcceptsZone(t *testing.T) {
	breached := []byte(`{"type":"zone","sequence":5,"payload":{"outside":true,"rect":{"x":0.3,"y":0.25,"width":0.4,"height":0.3},"detectedAt":1769000000000}}`)
	if _, err := ValidateEnvelope(breached); err != nil {
		t.Fatal(err)
	}

	back := []byte(`{"type":"zone","sequence":6,"payload":{"outside":false,"rect":{"x":0.3,"y":0.25,"width":0.4,"height":0.3},"detectedAt":1769000000000}}`)
	if _, err := ValidateEnvelope(back); err != nil {
		t.Fatal(err)
	}

	// 安全区被取消：不再报警且不带矩形，网页据此停止画框。
	cleared := []byte(`{"type":"zone","sequence":7,"payload":{"outside":false,"rect":null,"detectedAt":1769000000000}}`)
	if _, err := ValidateEnvelope(cleared); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnvelopeAcceptsZoneRectTouchingTheEdges(t *testing.T) {
	// 铺满整张小地图是合法配置，浮点边界不应被误判越界。
	full := []byte(`{"type":"zone","sequence":8,"payload":{"outside":true,"rect":{"x":0,"y":0,"width":1,"height":1},"detectedAt":1769000000000}}`)
	if _, err := ValidateEnvelope(full); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnvelopeRejectsMalformedZone(t *testing.T) {
	cases := map[string]string{
		"缺少时间戳":   `{"type":"zone","sequence":1,"payload":{"outside":true,"rect":{"x":0.3,"y":0.3,"width":0.4,"height":0.4},"detectedAt":0}}`,
		"报警却没有矩形": `{"type":"zone","sequence":1,"payload":{"outside":true,"rect":null,"detectedAt":1769000000000}}`,
		"矩形超出右边界": `{"type":"zone","sequence":1,"payload":{"outside":true,"rect":{"x":0.8,"y":0.3,"width":0.4,"height":0.4},"detectedAt":1769000000000}}`,
		"矩形超出下边界": `{"type":"zone","sequence":1,"payload":{"outside":true,"rect":{"x":0.3,"y":0.8,"width":0.4,"height":0.4},"detectedAt":1769000000000}}`,
		"矩形起点为负":  `{"type":"zone","sequence":1,"payload":{"outside":true,"rect":{"x":-0.1,"y":0.3,"width":0.4,"height":0.4},"detectedAt":1769000000000}}`,
		"矩形宽度为零":  `{"type":"zone","sequence":1,"payload":{"outside":true,"rect":{"x":0.3,"y":0.3,"width":0,"height":0.4},"detectedAt":1769000000000}}`,
	}
	for name, message := range cases {
		if _, err := ValidateEnvelope([]byte(message)); err == nil {
			t.Fatalf("expected %s 的安全区消息被拒绝", name)
		}
	}
}

func TestValidateEnvelopeAcceptsClientState(t *testing.T) {
	message := []byte(`{"type":"client_state","sequence":1,"payload":{"mode":"follow_heal","running":true}}`)
	envelope, err := ValidateEnvelope(message)
	if err != nil {
		t.Fatalf("expected valid client state, got %v", err)
	}
	if envelope.Type != TypeClientState {
		t.Fatalf("expected %s, got %s", TypeClientState, envelope.Type)
	}
}

func TestValidateEnvelopeAcceptsTempleClientState(t *testing.T) {
	message := []byte(`{"type":"client_state","sequence":1,"payload":{"mode":"temple","running":true}}`)
	if _, err := ValidateEnvelope(message); err != nil {
		t.Fatalf("expected temple client state to remain connected, got %v", err)
	}
}

func TestValidateEnvelopeAcceptsTeamJoined(t *testing.T) {
	message := []byte(`{"type":"team_joined","sequence":2,"payload":{"teamId":7,"roleName":"测试角色"}}`)
	if _, err := ValidateEnvelope(message); err != nil {
		t.Fatalf("expected valid team joined receipt, got %v", err)
	}
}

func TestValidateEnvelopeRejectsInvalidTeamJoined(t *testing.T) {
	cases := []string{
		`{"type":"team_joined","sequence":2,"payload":{"teamId":0,"roleName":"测试角色"}}`,
		`{"type":"team_joined","sequence":2,"payload":{"teamId":7,"roleName":""}}`,
	}
	for _, message := range cases {
		if _, err := ValidateEnvelope([]byte(message)); err == nil {
			t.Fatal("expected invalid team joined receipt to be rejected")
		}
	}
}
