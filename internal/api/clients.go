package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"autobuff-monitor/server/internal/protocol"

	"github.com/coder/websocket"
)

type clientItem struct {
	ID         string `json:"id"`
	ClientID   string `json:"clientId"`
	Name       string `json:"name"`
	Mode       string `json:"mode"`
	Running    bool   `json:"running"`
	Online     bool   `json:"online"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeenAt *int64 `json:"lastSeenAt"`
}

func (s *Server) clientItems(ctx context.Context, userID int64) ([]clientItem, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, client_id, name, mode, running, created_at, last_publish_at
		 FROM monitor_sessions
		 WHERE user_id = ? AND status = 1 AND client_id IS NOT NULL
		 ORDER BY last_publish_at DESC, created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]clientItem, 0)
	for rows.Next() {
		var item clientItem
		var running uint8
		var createdAt time.Time
		var lastSeen sql.NullTime
		if err := rows.Scan(&item.ID, &item.ClientID, &item.Name, &item.Mode, &running, &createdAt, &lastSeen); err != nil {
			return nil, err
		}
		item.Running = running == 1
		item.CreatedAt = createdAt.UnixMilli()
		online, state, _ := s.hub.ClientStatus(item.ID)
		item.Online = online
		if item.Online && state.Mode != "" {
			item.Mode = state.Mode
			item.Running = state.Running
		}
		if lastSeen.Valid {
			value := lastSeen.Time.UnixMilli()
			item.LastSeenAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	items, err := s.clientItems(r.Context(), mustUser(r.Context()).ID)
	if err != nil {
		s.internalError(w, "list clients failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": items})
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionId"))
	if len(sessionID) != 36 {
		writeError(w, http.StatusBadRequest, "invalid_session_id", "客户端记录编号无效")
		return
	}
	userID := mustUser(r.Context()).ID
	removed, err := s.revokeClient(r.Context(), userID, sessionID)
	if err != nil {
		s.internalError(w, "delete client failed", err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "client_not_found", "客户端不存在或已解绑")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeClient(ctx context.Context, userID int64, sessionID string) (bool, error) {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE monitor_sessions
		 SET status = 0, running = 0, revoked_at = NOW(3)
		 WHERE id = ? AND user_id = ? AND client_id IS NOT NULL AND status = 1`,
		sessionID, userID,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	s.hub.DisconnectDevice(sessionID)
	s.hub.NotifyClients(userID)
	return true, nil
}

type clientSocketRequest struct {
	Type     string `json:"type"`
	ClientID string `json:"clientId"`
	Action   string `json:"action"`
}

func (s *Server) handleClientsWebSocket(w http.ResponseWriter, r *http.Request) {
	rawToken := strings.TrimSpace(r.URL.Query().Get("access_token"))
	user, ok := s.authenticateToken(w, r, rawToken)
	if !ok {
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(4 * 1024)

	observerID, _ := randomUUID()
	updates, unsubscribe := s.hub.SubscribeClients(user.ID, observerID)
	defer unsubscribe()

	type readResult struct {
		body []byte
		err  error
	}
	incoming := make(chan readResult)
	go func() {
		for {
			_, body, err := connection.Read(r.Context())
			select {
			case incoming <- readResult{body: body, err: err}:
			case <-r.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	writeClients := func() error {
		items, err := s.clientItems(r.Context(), user.ID)
		if err != nil {
			return err
		}
		body, err := json.Marshal(map[string]any{"type": "clients", "clients": items})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		return connection.Write(ctx, websocket.MessageText, body)
	}

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case _, ok := <-updates:
			if !ok || writeClients() != nil {
				return
			}
		case result := <-incoming:
			if result.err != nil {
				return
			}
			var request clientSocketRequest
			if json.Unmarshal(result.body, &request) != nil ||
				request.Type != "command" ||
				(request.Action != "start" && request.Action != "stop") {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid command")
				return
			}
			var sessionID string
			err := s.db.QueryRowContext(
				r.Context(),
				`SELECT id FROM monitor_sessions
				 WHERE user_id = ? AND client_id = ? AND status = 1 LIMIT 1`,
				user.ID, request.ClientID,
			).Scan(&sessionID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return
			}
			s.hub.SendCommand(sessionID, protocol.ClientCommand{
				Type:   "command",
				Action: request.Action,
			})
		case <-pingTicker.C:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			err := connection.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
