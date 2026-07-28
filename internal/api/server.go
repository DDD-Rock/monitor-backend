package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

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
	userContextKey         contextKey = "authenticated-user"
	registrationInviteCode            = "XIAOXIN"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)

type authenticatedUser struct {
	ID       int64
	Username string
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
	mux.Handle("POST /api/monitor/sessions", s.requireAuth(http.HandlerFunc(s.handleCreateSession)))
	mux.Handle("GET /api/monitor/sessions/current", s.requireAuth(http.HandlerFunc(s.handleCurrentSession)))
	mux.Handle("DELETE /api/monitor/sessions/current", s.requireAuth(http.HandlerFunc(s.handleRevokeSession)))
	mux.Handle("GET /api/notifications/bark", s.requireAuth(http.HandlerFunc(s.handleBarkSettings)))
	mux.Handle("PUT /api/notifications/bark", s.requireAuth(http.HandlerFunc(s.handleSaveBark)))
	mux.Handle("POST /api/notifications/bark/test", s.requireAuth(http.HandlerFunc(s.handleBarkTest)))
	mux.HandleFunc("GET /api/preview/notifications/bark", s.handlePreviewBarkSettings)
	mux.HandleFunc("PUT /api/preview/notifications/exp-stalled", s.handlePreviewEXPStalled)
	mux.HandleFunc("PUT /api/preview/notifications/rune-alert", s.handlePreviewRuneAlert)
	mux.HandleFunc("PUT /api/preview/notifications/zone-breach", s.handlePreviewZoneBreach)
	mux.HandleFunc("POST /api/preview/notifications/bark/test", s.handlePreviewBarkTest)
	mux.HandleFunc("GET /api/preview/exp-gain", s.handlePreviewEXPGain)
	mux.HandleFunc("POST /api/preview/exp-gain/reset-total", s.handlePreviewResetEXPGainTotal)
	mux.Handle("GET /ws/device", s.requireAuth(http.HandlerFunc(s.handleDeviceWebSocket)))
	mux.HandleFunc("GET /ws/view", s.handleViewerWebSocket)
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
	if !validRegistrationInviteCode(request.InviteCode) {
		writeError(w, http.StatusForbidden, "invalid_invite_code", "邀请码无效")
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	if !usernamePattern.MatchString(request.Username) {
		writeError(w, http.StatusBadRequest, "invalid_username", "用户名须为 3–32 位字母、数字或下划线")
		return
	}
	if len(request.Password) < 8 || len(request.Password) > 72 {
		writeError(w, http.StatusBadRequest, "invalid_password", "密码长度须为 8–72 位")
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		s.internalError(w, "password hashing failed", err)
		return
	}
	result, err := s.db.ExecContext(
		r.Context(),
		`INSERT INTO users (username, password_hash) VALUES (?, ?)`,
		request.Username,
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
	s.respondWithAccessToken(w, userID, request.Username, http.StatusCreated)
}

func validRegistrationInviteCode(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), registrationInviteCode)
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
	_, _ = s.db.ExecContext(r.Context(), `UPDATE users SET last_login_at = NOW(3) WHERE id = ?`, userID)
	s.respondWithAccessToken(w, userID, request.Username, http.StatusOK)
}

func (s *Server) respondWithAccessToken(w http.ResponseWriter, userID int64, username string, status int) {
	token, expiresAt, err := s.auth.Issue(userID, username)
	if err != nil {
		s.internalError(w, "issue access token failed", err)
		return
	}
	writeJSON(w, status, map[string]any{
		"accessToken": token,
		"expiresAt":   expiresAt.UnixMilli(),
		"user": map[string]any{
			"id":       userID,
			"username": username,
		},
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       user.ID,
		"username": user.Username,
	})
}

type createSessionRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request createSessionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		request.Name = "我的电脑"
	}
	if len([]rune(request.Name)) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_name", "设备名称不能超过 64 个字符")
		return
	}

	sessionID, err := randomUUID()
	if err != nil {
		s.internalError(w, "generate session id failed", err)
		return
	}
	previewToken, previewHash, err := randomPreviewToken()
	if err != nil {
		s.internalError(w, "generate preview token failed", err)
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalError(w, "start session transaction failed", err)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(
		r.Context(),
		`UPDATE monitor_sessions SET status = 0, revoked_at = NOW(3) WHERE user_id = ? AND status = 1`,
		user.ID,
	); err != nil {
		s.internalError(w, "revoke previous session failed", err)
		return
	}
	if _, err := tx.ExecContext(
		r.Context(),
		`INSERT INTO monitor_sessions (id, user_id, name, preview_token_hash) VALUES (?, ?, ?, ?)`,
		sessionID,
		user.ID,
		request.Name,
		previewHash[:],
	); err != nil {
		s.internalError(w, "create monitor session failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.internalError(w, "commit monitor session failed", err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           sessionID,
		"name":         request.Name,
		"previewToken": previewToken,
		"previewURL":   s.cfg.PublicBaseURL + "/preview/" + previewToken,
		"publishURL":   websocketURL(s.cfg.PublicBaseURL) + "/ws/device?session_id=" + sessionID,
	})
}

func (s *Server) handleCurrentSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var sessionID, name string
	var createdAt time.Time
	err := s.db.QueryRowContext(
		r.Context(),
		`SELECT id, name, created_at FROM monitor_sessions WHERE user_id = ? AND status = 1 ORDER BY created_at DESC LIMIT 1`,
		user.ID,
	).Scan(&sessionID, &name, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"session": nil})
		return
	}
	if err != nil {
		s.internalError(w, "query monitor session failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"id":        sessionID,
			"name":      name,
			"createdAt": createdAt.UnixMilli(),
		},
	})
}

func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	_, err := s.db.ExecContext(
		r.Context(),
		`UPDATE monitor_sessions SET status = 0, revoked_at = NOW(3) WHERE user_id = ? AND status = 1`,
		user.ID,
	)
	if err != nil {
		s.internalError(w, "revoke monitor session failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeviceWebSocket(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_session", "缺少监控会话")
		return
	}
	var exists int
	if err := s.db.QueryRowContext(
		r.Context(),
		`SELECT 1 FROM monitor_sessions WHERE id = ? AND user_id = ? AND status = 1`,
		sessionID,
		user.ID,
	).Scan(&exists); err != nil {
		writeError(w, http.StatusForbidden, "invalid_session", "监控会话无效")
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(protocol.MaxMessageBytes)

	connectionID, _ := randomUUID()
	detach := s.hub.AttachPublisher(sessionID, connectionID)
	defer detach()
	_, _ = s.db.ExecContext(r.Context(), `UPDATE monitor_sessions SET last_publish_at = NOW(3) WHERE id = ?`, sessionID)

	for {
		_, message, err := connection.Read(r.Context())
		if err != nil {
			return
		}
		envelope, err := protocol.ValidateEnvelope(message)
		if err != nil {
			_ = connection.Close(websocket.StatusPolicyViolation, err.Error())
			return
		}
		s.hub.Publish(sessionID, envelope, message)
	}
}

func (s *Server) handleViewerWebSocket(w http.ResponseWriter, r *http.Request) {
	rawToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if rawToken == "" {
		writeError(w, http.StatusUnauthorized, "missing_preview_token", "预览链接无效")
		return
	}
	tokenHash := sha256.Sum256([]byte(rawToken))
	var sessionID string
	if err := s.db.QueryRowContext(
		r.Context(),
		`SELECT id FROM monitor_sessions WHERE preview_token_hash = ? AND status = 1 LIMIT 1`,
		tokenHash[:],
	).Scan(&sessionID); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_preview_token", "预览链接无效或已失效")
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connectionContext := connection.CloseRead(r.Context())

	viewerID, _ := randomUUID()
	messages, unsubscribe := s.hub.Subscribe(sessionID, viewerID)
	defer unsubscribe()

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
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

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "missing_token", "请先登录")
			return
		}
		userID, username, err := s.auth.Parse(raw)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid_token", "登录已失效，请重新登录")
			return
		}
		var activeUsername string
		if err := s.db.QueryRowContext(
			r.Context(),
			`SELECT username FROM users WHERE id = ? AND status = 1 LIMIT 1`,
			userID,
		).Scan(&activeUsername); err != nil {
			writeError(w, http.StatusUnauthorized, "account_disabled", "账号不可用，请重新登录")
			return
		}
		if activeUsername != "" {
			username = activeUsername
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(
			r.Context(),
			userContextKey,
			authenticatedUser{ID: userID, Username: username},
		)))
	})
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

func randomPreviewToken() (string, [32]byte, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	return token, sha256.Sum256([]byte(token)), nil
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

func websocketURL(publicBaseURL string) string {
	if strings.HasPrefix(publicBaseURL, "https://") {
		return "wss://" + strings.TrimPrefix(publicBaseURL, "https://")
	}
	return "ws://" + strings.TrimPrefix(publicBaseURL, "http://")
}
