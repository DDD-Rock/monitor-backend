package protocol

import (
	"encoding/json"
	"errors"
)

const (
	MaxMessageBytes = 16 * 1024
	TypeMap         = "map"
	TypeFrame       = "frame"
	TypeStatus      = "status"
	TypeEXP         = "exp"
	TypeRune        = "rune"
	TypeSnapshot    = "snapshot"
)

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Platform struct {
	ID     string  `json:"id"`
	Points []Point `json:"points"`
}

type Rope struct {
	ID      string  `json:"id"`
	X       float64 `json:"x"`
	TopY    float64 `json:"topY"`
	BottomY float64 `json:"bottomY"`
}

type Portal struct {
	ID    string `json:"id"`
	Point Point  `json:"point"`
	Type  string `json:"type"`
}

type MapPayload struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	AspectRatio float64    `json:"aspectRatio"`
	Platforms   []Platform `json:"platforms"`
	Ropes       []Rope     `json:"ropes"`
	Portals     []Portal   `json:"portals"`
}

type FramePayload struct {
	Player     *Point  `json:"player"`
	Teammates  []Point `json:"teammates"`
	Others     []Point `json:"others"`
	SourceFPS  float64 `json:"sourceFPS"`
	CapturedAt int64   `json:"capturedAt"`
}

type StatusPayload struct {
	Online  bool   `json:"online"`
	Message string `json:"message"`
}

type EXPPayload struct {
	CurrentEXP   *int64   `json:"currentEXP"`
	Percent      *float64 `json:"percent"`
	Confidence   *float64 `json:"confidence"`
	Status       string   `json:"status"`
	RecognizedAt int64    `json:"recognizedAt"`
}

// RunePayload 描述 Mac 端对「符文诅咒提示横幅」的识别结果。
// Detected 为 true 表示画面上仍挂着紫色符文提示，需要尽快解除。
type RunePayload struct {
	Detected   bool     `json:"detected"`
	Confidence *float64 `json:"confidence"`
	DetectedAt int64    `json:"detectedAt"`
}

type Envelope struct {
	Type     string          `json:"type"`
	Sequence uint64          `json:"sequence"`
	Payload  json.RawMessage `json:"payload"`
}

type Snapshot struct {
	Type      string          `json:"type"`
	Online    bool            `json:"online"`
	Map       json.RawMessage `json:"map,omitempty"`
	Frame     json.RawMessage `json:"frame,omitempty"`
	Status    json.RawMessage `json:"status,omitempty"`
	EXP       json.RawMessage `json:"exp,omitempty"`
	Rune      json.RawMessage `json:"rune,omitempty"`
	UpdatedAt int64           `json:"updatedAt"`
}

func ValidateEnvelope(message []byte) (Envelope, error) {
	if len(message) == 0 || len(message) > MaxMessageBytes {
		return Envelope{}, errors.New("message size is invalid")
	}
	var envelope Envelope
	if err := json.Unmarshal(message, &envelope); err != nil {
		return Envelope{}, errors.New("invalid JSON message")
	}
	switch envelope.Type {
	case TypeMap:
		var payload MapPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.ID == "" || payload.Name == "" {
			return Envelope{}, errors.New("invalid map payload")
		}
	case TypeFrame:
		var payload FramePayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return Envelope{}, errors.New("invalid frame payload")
		}
		if payload.Player != nil && !validPoint(*payload.Player) {
			return Envelope{}, errors.New("invalid player point")
		}
		for _, point := range append(payload.Teammates, payload.Others...) {
			if !validPoint(point) {
				return Envelope{}, errors.New("invalid marker point")
			}
		}
	case TypeStatus:
		var payload StatusPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return Envelope{}, errors.New("invalid status payload")
		}
	case TypeEXP:
		var payload EXPPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return Envelope{}, errors.New("invalid EXP payload")
		}
		hasReading := payload.CurrentEXP != nil || payload.Percent != nil || payload.Confidence != nil
		if hasReading && (payload.CurrentEXP == nil || payload.Percent == nil || payload.Confidence == nil) {
			return Envelope{}, errors.New("incomplete EXP payload")
		}
		if payload.CurrentEXP != nil && (*payload.CurrentEXP < 0 ||
			*payload.Percent < 0 || *payload.Percent > 100 ||
			*payload.Confidence < 0 || *payload.Confidence > 1) {
			return Envelope{}, errors.New("invalid EXP values")
		}
	case TypeRune:
		var payload RunePayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return Envelope{}, errors.New("invalid rune payload")
		}
		if payload.DetectedAt <= 0 {
			return Envelope{}, errors.New("invalid rune timestamp")
		}
		if payload.Confidence != nil && (*payload.Confidence < 0 || *payload.Confidence > 1) {
			return Envelope{}, errors.New("invalid rune confidence")
		}
		if !payload.Detected && payload.Confidence != nil {
			return Envelope{}, errors.New("cleared rune must not carry confidence")
		}
	default:
		return Envelope{}, errors.New("unsupported message type")
	}
	return envelope, nil
}

func validPoint(point Point) bool {
	return point.X >= 0 && point.X <= 1 && point.Y >= 0 && point.Y <= 1
}
