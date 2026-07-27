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
	TypeZone        = "zone"
	TypeSnapshot    = "snapshot"
)

// 归一化坐标允许的浮点误差。客户端由像素换算而来，边界上会有微小偏差。
const normalizedEpsilon = 1e-6

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

// ZoneRect 是归一化的安全区矩形，原点在小地图左上角，四个值都在 0~1。
//
// 客户端按「基准点为中心 + 长宽」配置，上报前换算成左上角加宽高，
// 这样网页拿到就能直接画框，不用再关心中心点语义。
type ZoneRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (r ZoneRect) valid() bool {
	if r.Width <= 0 || r.Height <= 0 {
		return false
	}
	if r.X < -normalizedEpsilon || r.Y < -normalizedEpsilon {
		return false
	}
	return r.X+r.Width <= 1+normalizedEpsilon && r.Y+r.Height <= 1+normalizedEpsilon
}

// ZonePayload 描述 Mac 端对「角色是否离开安全区」的判定结果。
// Outside 为 true 表示黄点已经跑出矩形范围，需要报警。
// Rect 为空表示安全区已被取消，网页应停止画框。
type ZonePayload struct {
	Outside    bool      `json:"outside"`
	Rect       *ZoneRect `json:"rect"`
	DetectedAt int64     `json:"detectedAt"`
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
	Zone      json.RawMessage `json:"zone,omitempty"`
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
	case TypeZone:
		var payload ZonePayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return Envelope{}, errors.New("invalid zone payload")
		}
		if payload.DetectedAt <= 0 {
			return Envelope{}, errors.New("invalid zone timestamp")
		}
		if payload.Rect != nil && !payload.Rect.valid() {
			return Envelope{}, errors.New("invalid zone rect")
		}
		// 报警必须带上矩形，否则网页无从判断跑出了哪个范围。
		if payload.Outside && payload.Rect == nil {
			return Envelope{}, errors.New("zone breach must carry the rect")
		}
	default:
		return Envelope{}, errors.New("unsupported message type")
	}
	return envelope, nil
}

func validPoint(point Point) bool {
	return point.X >= 0 && point.X <= 1 && point.Y >= 0 && point.Y <= 1
}
