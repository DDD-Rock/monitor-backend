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
	userID       int64
	controls     chan []byte
	viewers      map[string]chan []byte
	latestMap    json.RawMessage
	latestFrame  json.RawMessage
	latestStatus json.RawMessage
	latestEXP    json.RawMessage
	latestRune   json.RawMessage
	latestZone   json.RawMessage
	latestGain   json.RawMessage
	clientState  protocol.ClientStatePayload
	connected    bool
	online       bool
	updatedAt    int64
}

type Hub struct {
	mu              sync.Mutex
	rooms           map[string]*Room
	clientObservers map[int64]map[string]chan struct{}
	closed          bool
}

func NewHub() *Hub {
	return &Hub{
		rooms:           make(map[string]*Room),
		clientObservers: make(map[int64]map[string]chan struct{}),
	}
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
	_, detach := h.AttachDevice(sessionID, 0, connectionID)
	return detach
}

func (h *Hub) AttachDevice(sessionID string, userID int64, connectionID string) (<-chan []byte, func()) {
	room := h.room(sessionID)
	room.mu.Lock()
	room.publisher = connectionID
	room.userID = userID
	room.controls = make(chan []byte, 8)
	room.connected = true
	room.online = userID == 0 || (room.clientState.Mode == "monitor" && room.clientState.Running)
	room.updatedAt = time.Now().UnixMilli()
	controls := room.controls
	room.mu.Unlock()
	room.broadcastSnapshot()
	h.notifyClientObservers(userID)

	return controls, func() {
		room.mu.Lock()
		if room.publisher == connectionID {
			room.publisher = ""
			room.controls = nil
			room.connected = false
			room.online = false
			room.updatedAt = time.Now().UnixMilli()
		}
		room.mu.Unlock()
		room.broadcastSnapshot()
		h.notifyClientObservers(userID)
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
	case protocol.TypeClientState:
		_ = json.Unmarshal(envelope.Payload, &room.clientState)
		room.online = room.clientState.Mode == "monitor" && room.clientState.Running
	}
	room.updatedAt = time.Now().UnixMilli()
	room.mu.Unlock()
	room.broadcast(raw)
	if envelope.Type == protocol.TypeClientState {
		h.notifyClientObservers(room.userID)
	}
}

func (h *Hub) SendCommand(sessionID string, command protocol.ClientCommand) bool {
	body, err := json.Marshal(command)
	if err != nil {
		return false
	}
	h.mu.Lock()
	room := h.rooms[sessionID]
	h.mu.Unlock()
	if room == nil {
		return false
	}
	room.mu.RLock()
	controls := room.controls
	connected := room.connected
	room.mu.RUnlock()
	if !connected || controls == nil {
		return false
	}
	select {
	case controls <- body:
		return true
	default:
		return false
	}
}

func (h *Hub) ClientStatus(sessionID string) (online bool, state protocol.ClientStatePayload, updatedAt int64) {
	h.mu.Lock()
	room := h.rooms[sessionID]
	h.mu.Unlock()
	if room == nil {
		return false, protocol.ClientStatePayload{}, 0
	}
	room.mu.RLock()
	defer room.mu.RUnlock()
	return room.connected, room.clientState, room.updatedAt
}

func (h *Hub) SubscribeClients(userID int64, observerID string) (<-chan struct{}, func()) {
	channel := make(chan struct{}, 1)
	h.mu.Lock()
	if h.clientObservers[userID] == nil {
		h.clientObservers[userID] = make(map[string]chan struct{})
	}
	h.clientObservers[userID][observerID] = channel
	h.mu.Unlock()
	channel <- struct{}{}
	return channel, func() {
		h.mu.Lock()
		if observers := h.clientObservers[userID]; observers != nil {
			if existing, ok := observers[observerID]; ok {
				delete(observers, observerID)
				close(existing)
			}
			if len(observers) == 0 {
				delete(h.clientObservers, userID)
			}
		}
		h.mu.Unlock()
	}
}

func (h *Hub) notifyClientObservers(userID int64) {
	if userID <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, channel := range h.clientObservers[userID] {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
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
		room.controls = nil
		for id, channel := range room.viewers {
			delete(room.viewers, id)
			close(channel)
		}
		room.mu.Unlock()
	}
	for userID, observers := range h.clientObservers {
		for id, channel := range observers {
			delete(observers, id)
			close(channel)
		}
		delete(h.clientObservers, userID)
	}
}
