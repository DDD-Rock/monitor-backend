package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"autobuff-monitor/server/internal/auth"
	"autobuff-monitor/server/internal/config"
	"autobuff-monitor/server/internal/expgain"
	"autobuff-monitor/server/internal/notification"
	"autobuff-monitor/server/internal/protocol"
	"autobuff-monitor/server/internal/realtime"

	"github.com/coder/websocket"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const (
	userContextKey     contextKey = "authenticated-user"
	devicePingInterval            = 30 * time.Second
	devicePingTimeout             = 10 * time.Second
	devicePingFailures            = 2
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)
var inviteCodePattern = regexp.MustCompile(`^[A-Z0-9]{6}$`)

var errClientLimitReached = errors.New("client limit reached")
var errClientUnbound = errors.New("client unbound")

type authenticatedUser struct {
	ID           int64
	Username     string
	Nickname     string
	IsSuperAdmin bool
}

type Server struct {
	cfg     config.Config
	db      *sql.DB
	auth    *auth.Service
	hub     *realtime.Hub
	notify  *notification.Service
	expGain *expgain.Service
	logger  *slog.Logger
}

func NewServer(
	cfg config.Config,
	db *sql.DB,
	authService *auth.Service,
	hub *realtime.Hub,
	notificationService *notification.Service,
	expGainService *expgain.Service,
	logger *slog.Logger,
) *Server {
	return &Server{
		cfg:     cfg,
		db:      db,
		auth:    authService,
		hub:     hub,
		notify:  notificationService,
		expGain: expGainService,
		logger:  logger,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", s.handleHealth)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.Handle("GET /api/auth/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("GET /api/clients", s.requireAuth(http.HandlerFunc(s.handleClients)))
	mux.Handle("POST /api/clients/bind", s.requireAuth(http.HandlerFunc(s.handleBindClient)))
	mux.Handle("GET /api/clients/authorization", s.requireAuth(http.HandlerFunc(s.handleClientAuthorization)))
	mux.Handle("DELETE /api/clients/{sessionId}", s.requireAuth(http.HandlerFunc(s.handleDeleteClient)))
	mux.Handle("PUT /api/clients/role-name", s.requireAuth(http.HandlerFunc(s.handleSaveClientRoleName)))
	mux.Handle("GET /api/rope-team", s.requireAuth(http.HandlerFunc(s.handleRopeTeam)))
	mux.Handle("PUT /api/rope-team", s.requireAuth(http.HandlerFunc(s.handleSaveRopeTeam)))
	mux.Handle("DELETE /api/rope-team", s.requireAuth(http.HandlerFunc(s.handleDeleteRopeTeam)))
	mux.Handle("GET /api/admin/users", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminUsers)))
	mux.Handle("PATCH /api/admin/users/{id}/status", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminUserStatus)))
	mux.Handle("PUT /api/admin/users/{id}/password", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminUserPassword)))
	mux.Handle("GET /api/admin/users/{id}/clients", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminUserClients)))
	mux.Handle("DELETE /api/admin/users/{id}/clients/{sessionId}", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminDeleteUserClient)))
	mux.Handle("PATCH /api/admin/users/{id}/client-limit", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminUserClientLimit)))
	mux.Handle("GET /api/admin/invite-codes", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminInviteCodes)))
	mux.Handle("POST /api/admin/invite-codes", s.requireSuperAdmin(http.HandlerFunc(s.handleCreateAdminInviteCode)))
	mux.Handle("DELETE /api/admin/invite-codes/{id}", s.requireSuperAdmin(http.HandlerFunc(s.handleDeleteAdminInviteCode)))
	mux.Handle("GET /api/admin/client-versions", s.requireSuperAdmin(http.HandlerFunc(s.handleAdminClientVersions)))
	mux.Handle("PUT /api/admin/client-versions", s.requireSuperAdmin(http.HandlerFunc(s.handleSaveAdminClientVersion)))
	mux.Handle("GET /api/admin/maps", s.requireSuperAdmin(http.HandlerFunc(s.handleCloudMaps)))
	mux.Handle("POST /api/admin/maps", s.requireSuperAdmin(http.HandlerFunc(s.handleUploadCloudMaps)))
	mux.Handle("GET /api/admin/maps/{id}", s.requireSuperAdmin(http.HandlerFunc(s.handleDownloadCloudMap)))
	mux.Handle("DELETE /api/admin/maps/{id}", s.requireSuperAdmin(http.HandlerFunc(s.handleDeleteCloudMap)))
	mux.Handle("GET /api/notifications/bark", s.requireAuth(http.HandlerFunc(s.handleBarkSettings)))
	mux.Handle("PUT /api/notifications/bark", s.requireAuth(http.HandlerFunc(s.handleSaveBark)))
	mux.Handle("POST /api/notifications/bark/test", s.requireAuth(http.HandlerFunc(s.handleBarkTest)))
	mux.Handle("POST /api/notifications/bark/test-critical", s.requireAuth(http.HandlerFunc(s.handleBarkCriticalTest)))
	mux.Handle("PUT /api/notifications/exp-stalled", s.requireAuth(http.HandlerFunc(s.handleEXPStalled)))
	mux.Handle("PUT /api/notifications/rune-alert", s.requireAuth(http.HandlerFunc(s.handleRuneAlert)))
	mux.Handle("PUT /api/notifications/mouse-follow-verification", s.requireAuth(http.HandlerFunc(s.handleMouseFollowVerification)))
	mux.Handle("PUT /api/notifications/urgent-mute", s.requireAuth(http.HandlerFunc(s.handleUrgentMute)))
	mux.Handle("PUT /api/notifications/zone-breach", s.requireAuth(http.HandlerFunc(s.handleZoneBreach)))
	mux.Handle("GET /api/monitor/exp-gain", s.requireAuth(http.HandlerFunc(s.handleEXPGain)))
	mux.Handle("POST /api/monitor/exp-gain/reset-total", s.requireAuth(http.HandlerFunc(s.handleResetEXPGainTotal)))
	mux.Handle("GET /ws/device", s.requireAuth(http.HandlerFunc(s.handleDeviceWebSocket)))
	mux.HandleFunc("GET /ws/view", s.handleViewerWebSocket)
	mux.HandleFunc("GET /ws/clients", s.handleClientsWebSocket)
	return s.recoverPanic(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"registrationOpen": s.cfg.AllowRegistration,
		"time":             time.Now().UnixMilli(),
	})
}

type credentialsRequest struct {
	Username   string `json:"username"`
	Nickname   string `json:"nickname,omitempty"`
	Password   string `json:"password"`
	InviteCode string `json:"inviteCode,omitempty"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AllowRegistration {
		writeError(w, http.StatusForbidden, "registration_closed", "当前未开放注册")
		return
	}

	var request credentialsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.InviteCode = normalizeInviteCode(request.InviteCode)
	if !validInviteCode(request.InviteCode) {
		writeError(w, http.StatusForbidden, "invalid_invite_code", "邀请码无效")
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	if !usernamePattern.MatchString(request.Username) {
		writeError(w, http.StatusBadRequest, "invalid_username", "用户名须为 3–32 位字母、数字或下划线")
		return
	}
	request.Nickname = strings.TrimSpace(request.Nickname)
	if !validNickname(request.Nickname) {
		writeError(w, http.StatusBadRequest, "invalid_nickname", "昵称须为 1–24 个字符且不能包含控制字符")
		return
	}
	if len(request.Password) < 8 || len(request.Password) > 72 {
		writeError(w, http.StatusBadRequest, "invalid_password", "密码长度须为 8–72 位")
		return
	}
	var inviteAvailable int
	err := s.db.QueryRowContext(
		r.Context(),
		`SELECT 1 FROM invite_codes
		 WHERE code = ? AND used_at IS NULL AND expires_at > NOW(3)
		 LIMIT 1`,
		request.InviteCode,
	).Scan(&inviteAvailable)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusForbidden, "invalid_invite_code", "邀请码无效、已使用或已过期")
		return
	}
	if err != nil {
		s.internalError(w, "check registration invite code failed", err)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		s.internalError(w, "password hashing failed", err)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalError(w, "begin registration transaction failed", err)
		return
	}
	defer tx.Rollback()

	var inviteCodeID int64
	err = tx.QueryRowContext(
		r.Context(),
		`SELECT id FROM invite_codes
		 WHERE code = ? AND used_at IS NULL AND expires_at > NOW(3)
		 LIMIT 1 FOR UPDATE`,
		request.InviteCode,
	).Scan(&inviteCodeID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusForbidden, "invalid_invite_code", "邀请码无效、已使用或已过期")
		return
	}
	if err != nil {
		s.internalError(w, "validate registration invite code failed", err)
		return
	}

	result, err := tx.ExecContext(
		r.Context(),
		`INSERT INTO users (username, nickname, password_hash) VALUES (?, ?, ?)`,
		request.Username,
		request.Nickname,
		string(passwordHash),
	)
	if err != nil {
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			writeError(w, http.StatusConflict, "username_exists", "用户名已存在")
			return
		}
		s.internalError(w, "create user failed", err)
		return
	}
	userID, _ := result.LastInsertId()
	if _, err = tx.ExecContext(
		r.Context(),
		`UPDATE invite_codes SET used_by = ?, used_at = NOW(3) WHERE id = ? AND used_at IS NULL`,
		userID,
		inviteCodeID,
	); err != nil {
		s.internalError(w, "consume registration invite code failed", err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.internalError(w, "commit registration failed", err)
		return
	}
	s.respondWithAccessToken(w, userID, request.Username, http.StatusCreated)
}

func normalizeInviteCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func validInviteCode(value string) bool {
	return inviteCodePattern.MatchString(value)
}

func validNickname(value string) bool {
	runes := []rune(value)
	if len(runes) < 1 || len(runes) > 24 {
		return false
	}
	for _, item := range runes {
		if unicode.IsControl(item) {
			return false
		}
	}
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request credentialsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Username = strings.TrimSpace(request.Username)

	var userID int64
	var passwordHash string
	var status uint8
	err := s.db.QueryRowContext(
		r.Context(),
		`SELECT id, password_hash, status FROM users WHERE username = ? LIMIT 1`,
		request.Username,
	).Scan(&userID, &passwordHash, &status)
	if err != nil || status != 1 || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(request.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	if !s.requireAllowedClientVersion(w, r) {
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE users SET last_login_at = NOW(3) WHERE id = ?`, userID)
	s.respondWithAccessToken(w, userID, request.Username, http.StatusOK)
}

func (s *Server) respondWithAccessToken(w http.ResponseWriter, userID int64, username string, status int) {
	token, expiresAt, err := s.auth.Issue(userID, username)
	if err != nil {
		s.internalError(w, "issue access token failed", err)
		return
	}
	var isSuperAdmin uint8
	var nickname string
	_ = s.db.QueryRow(`SELECT nickname, is_super_admin FROM users WHERE id = ?`, userID).Scan(&nickname, &isSuperAdmin)
	writeJSON(w, status, map[string]any{
		"accessToken": token,
		"expiresAt":   expiresAt.UnixMilli(),
		"user": map[string]any{
			"id":           userID,
			"username":     username,
			"nickname":     nickname,
			"isSuperAdmin": isSuperAdmin == 1,
		},
	})
}

func (s *Server) handleBindClient(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ClientID string `json:"clientId"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.ClientID = strings.TrimSpace(request.ClientID)
	if len(request.ClientID) < 8 || len(request.ClientID) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_client_id", "客户端标识无效")
		return
	}
	sessionID, clientName, err := s.ensureDeviceSession(
		r.Context(),
		mustUser(r.Context()).ID,
		request.ClientID,
		true,
	)
	if err != nil {
		if errors.Is(err, errClientLimitReached) {
			writeError(w, http.StatusForbidden, "client_limit_reached", "该账号绑定的客户端数量已达到上限，请联系管理员解绑旧客户端或调整额度")
			return
		}
		s.internalError(w, "bind client failed", err)
		return
	}
	_, _ = s.db.ExecContext(
		r.Context(),
		`UPDATE monitor_sessions SET last_publish_at = NOW(3) WHERE id = ?`,
		sessionID,
	)
	var roleName string
	_ = s.db.QueryRowContext(
		r.Context(),
		`SELECT role_name FROM monitor_sessions WHERE id = ?`,
		sessionID,
	).Scan(&roleName)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       sessionID,
		"clientId": request.ClientID,
		"name":     clientName,
		"roleName": roleName,
	})
}

func (s *Server) handleClientAuthorization(w http.ResponseWriter, r *http.Request) {
	if !s.requireAllowedClientVersion(w, r) {
		return
	}
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if len(clientID) < 8 || len(clientID) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_client_id", "客户端标识无效")
		return
	}
	var sessionID, clientName, roleName string
	err := s.db.QueryRowContext(
		r.Context(),
		`SELECT id, name, role_name FROM monitor_sessions
		 WHERE user_id = ? AND client_id = ? AND status = 1 LIMIT 1`,
		mustUser(r.Context()).ID,
		clientID,
	).Scan(&sessionID, &clientName, &roleName)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusForbidden, "client_unbound", "当前客户端已被管理员解绑，请重新登录")
		return
	}
	if err != nil {
		s.internalError(w, "check client authorization failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": sessionID, "clientId": clientID, "name": clientName, "roleName": roleName,
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if !s.requireAllowedClientVersion(w, r) {
		return
	}
	user := mustUser(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"nickname":     user.Nickname,
		"isSuperAdmin": user.IsSuperAdmin,
	})
}

func (s *Server) ensureDeviceSession(
	ctx context.Context,
	userID int64,
	clientID string,
	allowRebind bool,
) (string, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback() }()

	// 锁定账号行，将同一账号首次连接时的“查询并创建”串行化。
	// 这样无需给历史 monitor_sessions 表增加生成列或唯一索引。
	var lockedUserID int64
	var maxClientCount uint32
	if err := tx.QueryRowContext(
		ctx,
		`SELECT id, max_client_count FROM users WHERE id = ? AND status = 1 FOR UPDATE`,
		userID,
	).Scan(&lockedUserID, &maxClientCount); err != nil {
		return "", "", err
	}

	var sessionID, name string
	var sessionStatus uint8
	err = tx.QueryRowContext(
		ctx,
		`SELECT id, name, status FROM monitor_sessions
		 WHERE user_id = ? AND client_id = ? LIMIT 1`,
		userID,
		clientID,
	).Scan(&sessionID, &name, &sessionStatus)
	if err == nil {
		if sessionStatus == 0 && !allowRebind {
			return "", "", errClientUnbound
		}
		if sessionStatus == 0 {
			var clientCount uint32
			if err := tx.QueryRowContext(
				ctx,
				`SELECT COUNT(*) FROM monitor_sessions
				 WHERE user_id = ? AND status = 1 AND client_id IS NOT NULL`,
				userID,
			).Scan(&clientCount); err != nil {
				return "", "", err
			}
			if clientCount >= maxClientCount {
				return "", "", errClientLimitReached
			}
			if _, err := tx.ExecContext(
				ctx,
				`UPDATE monitor_sessions
				 SET status = 1, running = 0, revoked_at = NULL
				 WHERE id = ?`,
				sessionID,
			); err != nil {
				return "", "", err
			}
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return sessionID, name, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}

	var clientCount uint32
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM monitor_sessions
		 WHERE user_id = ? AND status = 1 AND client_id IS NOT NULL`,
		userID,
	).Scan(&clientCount); err != nil {
		return "", "", err
	}
	if clientCount >= maxClientCount {
		return "", "", errClientLimitReached
	}

	sessionID, err = randomUUID()
	if err != nil {
		return "", "", err
	}
	for attempt := 0; attempt < 20; attempt++ {
		name = randomClientName()
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO monitor_sessions (id, user_id, client_id, name)
			 VALUES (?, ?, ?, ?)`,
			sessionID,
			userID,
			clientID,
			name,
		)
		if err == nil {
			break
		}
		var mysqlError *mysql.MySQLError
		if !errors.As(err, &mysqlError) || mysqlError.Number != 1062 {
			return "", "", err
		}
	}
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return sessionID, name, nil
}

func (s *Server) handleDeviceWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.requireAllowedClientVersion(w, r) {
		return
	}
	user := mustUser(r.Context())
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if len(clientID) < 8 || len(clientID) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_client_id", "客户端标识无效")
		return
	}
	sessionID, clientName, err := s.ensureDeviceSession(r.Context(), user.ID, clientID, false)
	if err != nil {
		if errors.Is(err, errClientLimitReached) {
			writeError(w, http.StatusForbidden, "client_limit_reached", "该账号绑定的客户端数量已达到上限，请联系管理员解绑旧客户端或调整额度")
			return
		}
		if errors.Is(err, errClientUnbound) {
			writeError(w, http.StatusForbidden, "client_unbound", "该客户端已被管理员解绑，请重新登录后再使用")
			return
		}
		s.internalError(w, "ensure publisher channel failed", err)
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(protocol.MaxMessageBytes)

	connectionID, _ := randomUUID()
	controls, detach := s.hub.AttachDevice(sessionID, user.ID, connectionID)
	defer detach()
	_, _ = s.db.ExecContext(r.Context(), `UPDATE monitor_sessions SET last_publish_at = NOW(3) WHERE id = ?`, sessionID)

	identity, _ := json.Marshal(map[string]any{
		"type":     "identity",
		"clientId": clientID,
		"name":     clientName,
	})
	if err := connection.Write(r.Context(), websocket.MessageText, identity); err != nil {
		return
	}

	type incomingMessage struct {
		body []byte
		err  error
	}
	incoming := make(chan incomingMessage)
	go func() {
		for {
			_, message, err := connection.Read(r.Context())
			select {
			case incoming <- incomingMessage{body: message, err: err}:
			case <-r.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	pingTicker := time.NewTicker(devicePingInterval)
	defer pingTicker.Stop()
	consecutivePingFailures := 0
	for {
		select {
		case item := <-incoming:
			if item.err != nil {
				return
			}
			// Any received message proves the device is alive, even when a
			// previous pong arrived just after the Ping deadline.
			consecutivePingFailures = 0
			envelope, err := protocol.ValidateEnvelope(item.body)
			if err != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, err.Error())
				return
			}
			accepted := s.hub.Publish(sessionID, envelope, item.body)
			if envelope.Type == protocol.TypeClientState {
				var state protocol.ClientStatePayload
				if json.Unmarshal(envelope.Payload, &state) == nil {
					if !accepted {
						state.Running = false
						s.hub.SendCommand(sessionID, protocol.ClientCommand{
							Type:   "command",
							Action: "stop",
							Reason: "monitor_conflict",
						})
					} else if state.Mode == "monitor" && state.Running {
						// Clear stale rows left by disconnected devices so database-backed
						// statistics and notifications see the same single active monitor.
						_, _ = s.db.ExecContext(
							r.Context(),
							`UPDATE monitor_sessions SET running = 0
							 WHERE user_id = ? AND id <> ? AND mode = 'monitor' AND running = 1`,
							user.ID, sessionID,
						)
					}
					_, _ = s.db.ExecContext(
						r.Context(),
						`UPDATE monitor_sessions SET mode = ?, running = ?, last_publish_at = NOW(3) WHERE id = ?`,
						state.Mode, state.Running, sessionID,
					)
				}
			}
			if envelope.Type == protocol.TypeTeamJoined {
				var joined protocol.TeamJoinedPayload
				if json.Unmarshal(envelope.Payload, &joined) == nil {
					result, updateErr := s.db.ExecContext(
						r.Context(),
						`UPDATE rope_team_members rtm
						 JOIN rope_teams rt ON rt.id = rtm.team_id
						 SET rtm.joined = 1, rtm.joined_at = NOW(3)
						 WHERE rtm.team_id = ? AND rtm.session_id = ? AND rt.user_id = ?`,
						joined.TeamID, sessionID, user.ID,
					)
					if updateErr == nil {
						if affected, _ := result.RowsAffected(); affected > 0 {
							s.hub.NotifyClients(user.ID)
						}
					}
				}
			}
		case command, ok := <-controls:
			if !ok || command == nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "client unbound")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			err := connection.Write(ctx, websocket.MessageText, command)
			cancel()
			if err != nil {
				return
			}
		case <-pingTicker.C:
			ctx, cancel := context.WithTimeout(r.Context(), devicePingTimeout)
			err := connection.Ping(ctx)
			cancel()
			if err != nil {
				consecutivePingFailures++
				if consecutivePingFailures >= devicePingFailures {
					return
				}
				continue
			}
			consecutivePingFailures = 0
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleViewerWebSocket(w http.ResponseWriter, r *http.Request) {
	rawToken := strings.TrimSpace(r.URL.Query().Get("access_token"))
	user, ok := s.authenticateToken(w, r, rawToken)
	if !ok {
		return
	}
	sessionID, err := s.viewerSession(r.Context(), user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "client_not_found", "尚未找到可监控的客户端")
		return
	}
	if err != nil {
		s.internalError(w, "find viewer channel failed", err)
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connectionContext := connection.CloseRead(r.Context())

	viewerID, _ := randomUUID()
	messages, unsubscribeViewer := s.hub.Subscribe(sessionID, viewerID)
	defer func() { unsubscribeViewer() }()

	updates, unsubscribeUpdates := s.hub.SubscribeClients(user.ID, viewerID)
	defer unsubscribeUpdates()

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case _, ok := <-updates:
			if !ok {
				return
			}
			nextSessionID, resolveErr := s.viewerSession(connectionContext, user.ID)
			if resolveErr != nil || nextSessionID == sessionID {
				continue
			}
			unsubscribeViewer()
			sessionID = nextSessionID
			messages, unsubscribeViewer = s.hub.Subscribe(sessionID, viewerID)
		case message, ok := <-messages:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(connectionContext, 5*time.Second)
			err := connection.Write(ctx, websocket.MessageText, message)
			cancel()
			if err != nil {
				return
			}
		case <-pingTicker.C:
			ctx, cancel := context.WithTimeout(connectionContext, 5*time.Second)
			err := connection.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		case <-connectionContext.Done():
			return
		}
	}
}

// viewerSession resolves the single account-level monitor channel. The in-memory
// active claim wins; the most recently publishing bound client is only the
// offline fallback while no client is actively monitoring.
func (s *Server) viewerSession(ctx context.Context, userID int64) (string, error) {
	if sessionID, active := s.hub.ActiveMonitorSession(userID); active {
		return sessionID, nil
	}
	var sessionID string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id FROM monitor_sessions WHERE user_id = ? AND status = 1
		 ORDER BY last_publish_at DESC, created_at DESC LIMIT 1`,
		userID,
	).Scan(&sessionID)
	return sessionID, err
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		user, ok := s.authenticateToken(w, r, raw)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(
			r.Context(),
			userContextKey,
			user,
		)))
	})
}

func (s *Server) authenticateToken(w http.ResponseWriter, r *http.Request, raw string) (authenticatedUser, bool) {
	if raw == "" {
		writeError(w, http.StatusUnauthorized, "missing_token", "请先登录")
		return authenticatedUser{}, false
	}
	userID, username, err := s.auth.Parse(raw)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "登录已失效，请重新登录")
		return authenticatedUser{}, false
	}
	var activeUsername string
	var nickname string
	var isSuperAdmin uint8
	if err := s.db.QueryRowContext(
		r.Context(),
		`SELECT username, nickname, is_super_admin FROM users WHERE id = ? AND status = 1 LIMIT 1`,
		userID,
	).Scan(&activeUsername, &nickname, &isSuperAdmin); err != nil {
		writeError(w, http.StatusUnauthorized, "account_disabled", "账号不可用，请重新登录")
		return authenticatedUser{}, false
	}
	if activeUsername != "" {
		username = activeUsername
	}
	return authenticatedUser{ID: userID, Username: username, Nickname: nickname, IsSuperAdmin: isSuperAdmin == 1}, true
}

func (s *Server) requireSuperAdmin(next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mustUser(r.Context()).IsSuperAdmin {
			writeError(w, http.StatusForbidden, "admin_required", "仅超级管理员可以执行此操作")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func mustUser(ctx context.Context) authenticatedUser {
	return ctx.Value(userContextKey).(authenticatedUser)
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "error", recovered, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) internalError(w http.ResponseWriter, message string, err error) {
	s.logger.Error(message, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "请求内容格式错误")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error":   code,
		"message": message,
	})
}

func randomUUID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}

func randomClientName() string {
	adjectives := []string{"追风", "月光", "薄荷", "星尘", "橘子", "云朵", "琥珀", "青柠", "银河", "晚霞", "松露", "泡泡"}
	animals := []string{"水獭", "浣熊", "狐狸", "企鹅", "海豹", "熊猫", "松鼠", "鲸鱼", "兔子", "猫头鹰", "羊驼", "小鹿"}
	randomBytes := make([]byte, 3)
	if _, err := rand.Read(randomBytes); err != nil {
		return fmt.Sprintf("漫游设备-%d", time.Now().UnixNano()%10000)
	}
	return fmt.Sprintf(
		"%s%s-%03d",
		adjectives[int(randomBytes[0])%len(adjectives)],
		animals[int(randomBytes[1])%len(animals)],
		int(randomBytes[2])*4+int(randomBytes[0])%4,
	)
}
