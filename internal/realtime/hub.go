package realtime

import (
	"encoding/json"
	"sync"
	"time"

	"autobuff-monitor/server/internal/protocol"
)

type Room struct {
	mu           sync.RWMutex
	publisher    string
	viewers      map[string]chan []byte
	latestMap    json.RawMessage
	latestFrame  json.RawMessage
	latestStatus json.RawMessage
	latestEXP    json.RawMessage
	latestRune   json.RawMessage
	latestZone   json.RawMessage
	latestGain   json.RawMessage
	online       bool
	updatedAt    int64
}

type Hub struct {
	mu     sync.Mutex
	rooms  map[string]*Room
	closed bool
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]*Room)}
}

func (h *Hub) room(sessionID string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[sessionID]
	if room == nil {
		room = &Room{viewers: make(map[string]chan []byte)}
		h.rooms[sessionID] = room
	}
	return room
}

func (h *Hub) AttachPublisher(sessionID, connectionID string) func() {
	room := h.room(sessionID)
	room.mu.Lock()
	room.publisher = connectionID
	room.online = true
	room.updatedAt = time.Now().UnixMilli()
	room.mu.Unlock()
	room.broadcastSnapshot()

	return func() {
		room.mu.Lock()
		if room.publisher == connectionID {
			room.publisher = ""
			room.online = false
			room.updatedAt = time.Now().UnixMilli()
		}
		room.mu.Unlock()
		room.broadcastSnapshot()
	}
}

func (h *Hub) Publish(sessionID string, envelope protocol.Envelope, raw []byte) {
	room := h.room(sessionID)
	room.mu.Lock()
	switch envelope.Type {
	case protocol.TypeMap:
		room.latestMap = append(room.latestMap[:0], envelope.Payload...)
	case protocol.TypeFrame:
		room.latestFrame = append(room.latestFrame[:0], envelope.Payload...)
	case protocol.TypeStatus:
		room.latestStatus = append(room.latestStatus[:0], envelope.Payload...)
	case protocol.TypeEXP:
		room.latestEXP = append(room.latestEXP[:0], envelope.Payload...)
	case protocol.TypeRune:
		room.latestRune = append(room.latestRune[:0], envelope.Payload...)
	case protocol.TypeZone:
		room.latestZone = append(room.latestZone[:0], envelope.Payload...)
	case protocol.TypeGain:
		room.latestGain = append(room.latestGain[:0], envelope.Payload...)
	}
	room.updatedAt = time.Now().UnixMilli()
	room.mu.Unlock()
	room.broadcast(raw)
}

// PublishGain 由服务端写入经验累计快照并广播给查看端。
// 设备端不会走这条路径；ValidateEnvelope 也故意不接受 gain，防止客户端伪造。
func (h *Hub) PublishGain(sessionID string, payload protocol.GainPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	envelope := protocol.Envelope{Type: protocol.TypeGain, Payload: body}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	h.Publish(sessionID, envelope, raw)
	return nil
}

func (h *Hub) Subscribe(sessionID, viewerID string) (<-chan []byte, func()) {
	room := h.room(sessionID)
	channel := make(chan []byte, 1)
	room.mu.Lock()
	room.viewers[viewerID] = channel
	room.mu.Unlock()
	if snapshot, err := json.Marshal(room.snapshot()); err == nil {
		channel <- snapshot
	}
	return channel, func() {
		room.mu.Lock()
		if existing, ok := room.viewers[viewerID]; ok {
			delete(room.viewers, viewerID)
			close(existing)
		}
		room.mu.Unlock()
	}
}

func (h *Hub) LatestEXP(sessionID string) (protocol.EXPPayload, bool, bool) {
	h.mu.Lock()
	room := h.rooms[sessionID]
	h.mu.Unlock()
	if room == nil {
		return protocol.EXPPayload{}, false, false
	}
	room.mu.RLock()
	raw := append(json.RawMessage(nil), room.latestEXP...)
	online := room.online
	room.mu.RUnlock()
	if len(raw) == 0 {
		return protocol.EXPPayload{}, online, false
	}
	var payload protocol.EXPPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return protocol.EXPPayload{}, online, false
	}
	return payload, online, true
}

// LatestRune 返回会话最近一次上报的符文状态、发布端是否在线，以及是否有过上报。
func (h *Hub) LatestRune(sessionID string) (protocol.RunePayload, bool, bool) {
	h.mu.Lock()
	room := h.rooms[sessionID]
	h.mu.Unlock()
	if room == nil {
		return protocol.RunePayload{}, false, false
	}
	room.mu.RLock()
	raw := append(json.RawMessage(nil), room.latestRune...)
	online := room.online
	room.mu.RUnlock()
	if len(raw) == 0 {
		return protocol.RunePayload{}, online, false
	}
	var payload protocol.RunePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return protocol.RunePayload{}, online, false
	}
	return payload, online, true
}

// LatestZone 返回会话最近一次上报的安全区状态、发布端是否在线，以及是否有过上报。
func (h *Hub) LatestZone(sessionID string) (protocol.ZonePayload, bool, bool) {
	h.mu.Lock()
	room := h.rooms[sessionID]
	h.mu.Unlock()
	if room == nil {
		return protocol.ZonePayload{}, false, false
	}
	room.mu.RLock()
	raw := append(json.RawMessage(nil), room.latestZone...)
	online := room.online
	room.mu.RUnlock()
	if len(raw) == 0 {
		return protocol.ZonePayload{}, online, false
	}
	var payload protocol.ZonePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return protocol.ZonePayload{}, online, false
	}
	return payload, online, true
}

func (r *Room) snapshot() protocol.Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return protocol.Snapshot{
		Type:      protocol.TypeSnapshot,
		Online:    r.online,
		Map:       append(json.RawMessage(nil), r.latestMap...),
		Frame:     append(json.RawMessage(nil), r.latestFrame...),
		Status:    append(json.RawMessage(nil), r.latestStatus...),
		EXP:       append(json.RawMessage(nil), r.latestEXP...),
		Rune:      append(json.RawMessage(nil), r.latestRune...),
		Zone:      append(json.RawMessage(nil), r.latestZone...),
		Gain:      append(json.RawMessage(nil), r.latestGain...),
		UpdatedAt: r.updatedAt,
	}
}

func (r *Room) broadcastSnapshot() {
	message, err := json.Marshal(r.snapshot())
	if err == nil {
		r.broadcast(message)
	}
}

func (r *Room) broadcast(message []byte) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, channel := range r.viewers {
		copyOfMessage := append([]byte(nil), message...)
		select {
		case channel <- copyOfMessage:
		default:
			select {
			case <-channel:
			default:
			}
			select {
			case channel <- copyOfMessage:
			default:
			}
		}
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for _, room := range h.rooms {
		room.mu.Lock()
		for id, channel := range room.viewers {
			delete(room.viewers, id)
			close(channel)
		}
		room.mu.Unlock()
	}
}
