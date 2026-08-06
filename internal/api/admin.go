package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type adminUserItem struct {
	ID              int64    `json:"id"`
	Nickname        string   `json:"nickname"`
	Status          uint8    `json:"status"`
	IsSuperAdmin    bool     `json:"isSuperAdmin"`
	CreatedAt       int64    `json:"createdAt"`
	LastLoginAt     *int64   `json:"lastLoginAt"`
	ConnectedCount  int      `json:"connectedClientCount"`
	MaxClientCount  uint32   `json:"maxClientCount"`
	AuthorizedModes []string `json:"authorizedModes"`
}

type userModePermissions struct {
	IsSuperAdmin uint8
	Dead         uint8
	Live         uint8
	Temple       uint8
	FollowHeal   uint8
	Monitor      uint8
}

func (p userModePermissions) authorizedModes() []string {
	if p.IsSuperAdmin == 1 {
		return []string{"dead", "live", "temple", "follow_heal", "monitor"}
	}
	modes := make([]string, 0, 5)
	if p.Dead == 1 {
		modes = append(modes, "dead")
	}
	if p.Live == 1 {
		modes = append(modes, "live")
	}
	if p.Temple == 1 {
		modes = append(modes, "temple")
	}
	if p.FollowHeal == 1 {
		modes = append(modes, "follow_heal")
	}
	if p.Monitor == 1 {
		modes = append(modes, "monitor")
	}
	return modes
}

func (p userModePermissions) allows(mode string) bool {
	if p.IsSuperAdmin == 1 {
		return true
	}
	switch mode {
	case "dead":
		return p.Dead == 1
	case "live":
		return p.Live == 1
	case "temple":
		return p.Temple == 1
	case "follow_heal":
		return p.FollowHeal == 1
	case "monitor":
		return p.Monitor == 1
	default:
		return false
	}
}

func (s *Server) loadUserModePermissions(ctx context.Context, userID int64) (userModePermissions, error) {
	var p userModePermissions
	err := s.db.QueryRowContext(ctx, `SELECT is_super_admin, dead_mode_enabled, live_mode_enabled,
		temple_mode_enabled, follow_heal_mode_enabled, monitor_mode_enabled FROM users WHERE id = ?`, userID).
		Scan(&p.IsSuperAdmin, &p.Dead, &p.Live, &p.Temple, &p.FollowHeal, &p.Monitor)
	return p, err
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(
		r.Context(),
		`SELECT u.id, u.nickname, u.status, u.is_super_admin, u.created_at, u.last_login_at,
		        u.max_client_count, u.dead_mode_enabled, u.live_mode_enabled, u.temple_mode_enabled,
		        u.follow_heal_mode_enabled, u.monitor_mode_enabled,
		        COUNT(CASE WHEN ms.status = 1 AND ms.client_id IS NOT NULL THEN 1 END)
		 FROM users u
		 LEFT JOIN monitor_sessions ms ON ms.user_id = u.id
		 GROUP BY u.id, u.nickname, u.status, u.is_super_admin, u.created_at, u.last_login_at,
		          u.max_client_count, u.dead_mode_enabled, u.live_mode_enabled, u.temple_mode_enabled,
		          u.follow_heal_mode_enabled, u.monitor_mode_enabled
		 ORDER BY u.created_at DESC`,
	)
	if err != nil {
		s.internalError(w, "list users failed", err)
		return
	}
	defer rows.Close()
	users := make([]adminUserItem, 0)
	for rows.Next() {
		var item adminUserItem
		var isSuperAdmin uint8
		var dead, live, temple, followHeal, monitor uint8
		var createdAt time.Time
		var lastLogin sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.Nickname, &item.Status, &isSuperAdmin,
			&createdAt, &lastLogin, &item.MaxClientCount, &dead, &live, &temple, &followHeal, &monitor,
			&item.ConnectedCount,
		); err != nil {
			s.internalError(w, "scan users failed", err)
			return
		}
		item.IsSuperAdmin = isSuperAdmin == 1
		item.AuthorizedModes = userModePermissions{IsSuperAdmin: isSuperAdmin, Dead: dead, Live: live, Temple: temple, FollowHeal: followHeal, Monitor: monitor}.authorizedModes()
		item.CreatedAt = createdAt.UnixMilli()
		if lastLogin.Valid {
			value := lastLogin.Time.UnixMilli()
			item.LastLoginAt = &value
		}
		users = append(users, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleAdminUserClients(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestedUserID(w, r)
	if !ok {
		return
	}
	var maxClientCount uint32
	if err := s.db.QueryRowContext(
		r.Context(),
		`SELECT max_client_count FROM users WHERE id = ?`,
		userID,
	).Scan(&maxClientCount); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user_not_found", "用户不存在")
		return
	} else if err != nil {
		s.internalError(w, "load user client limit failed", err)
		return
	}
	items, err := s.clientItems(r.Context(), userID)
	if err != nil {
		s.internalError(w, "list user clients failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"clients":        items,
		"maxClientCount": maxClientCount,
	})
}

func (s *Server) handleAdminDeleteUserClient(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestedUserID(w, r)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("sessionId"))
	if len(sessionID) != 36 {
		writeError(w, http.StatusBadRequest, "invalid_session_id", "客户端记录编号无效")
		return
	}
	removed, err := s.revokeClient(r.Context(), userID, sessionID)
	if err != nil {
		s.internalError(w, "delete user client failed", err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "client_not_found", "客户端不存在或已解绑")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminUserClientLimit(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestedUserID(w, r)
	if !ok {
		return
	}
	var request struct {
		MaxClientCount *uint32 `json:"maxClientCount"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.MaxClientCount == nil || *request.MaxClientCount > 100 {
		writeError(w, http.StatusBadRequest, "invalid_client_limit", "客户端数量上限必须在 0 到 100 之间")
		return
	}
	result, err := s.db.ExecContext(
		r.Context(),
		`UPDATE users SET max_client_count = ? WHERE id = ?`,
		*request.MaxClientCount, userID,
	)
	if err != nil {
		s.internalError(w, "update user client limit failed", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var exists int
		err = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user_not_found", "用户不存在")
			return
		}
		if err != nil {
			s.internalError(w, "check user failed", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminUserAuthorizedModes(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestedUserID(w, r)
	if !ok {
		return
	}
	var request struct {
		Modes []string `json:"modes"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	allowed := map[string]bool{"dead": false, "live": false, "temple": false, "follow_heal": false, "monitor": false}
	for _, mode := range request.Modes {
		if _, exists := allowed[mode]; !exists {
			writeError(w, http.StatusBadRequest, "invalid_mode", "模式授权无效")
			return
		}
		allowed[mode] = true
	}
	result, err := s.db.ExecContext(r.Context(), `UPDATE users SET dead_mode_enabled=?, live_mode_enabled=?, temple_mode_enabled=?, follow_heal_mode_enabled=?, monitor_mode_enabled=? WHERE id=?`, boolToUint(allowed["dead"]), boolToUint(allowed["live"]), boolToUint(allowed["temple"]), boolToUint(allowed["follow_heal"]), boolToUint(allowed["monitor"]), userID)
	if err != nil {
		s.internalError(w, "update user mode permissions failed", err)
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		writeError(w, http.StatusNotFound, "user_not_found", "用户不存在")
		return
	}
	s.hub.NotifyClients(userID)
	w.WriteHeader(http.StatusNoContent)
}

func boolToUint(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}

func (s *Server) handleAdminKickUserClients(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestedUserID(w, r)
	if !ok {
		return
	}
	var request struct {
		SessionIDs []string `json:"sessionIds"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	kicked := 0
	for _, sessionID := range request.SessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if len(sessionID) != 36 {
			continue
		}
		var exists int
		err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM monitor_sessions WHERE id=? AND user_id=? AND status=1`, sessionID, userID).Scan(&exists)
		if err != nil {
			continue
		}
		if s.hub.KickDevice(sessionID) {
			kicked++
		}
	}
	s.hub.NotifyClients(userID)
	writeJSON(w, http.StatusOK, map[string]int{"kickedCount": kicked})
}

func requestedUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || value <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_user_id", "用户编号无效")
		return 0, false
	}
	return value, true
}

func (s *Server) handleAdminUserStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestedUserID(w, r)
	if !ok {
		return
	}
	if userID == mustUser(r.Context()).ID {
		writeError(w, http.StatusBadRequest, "cannot_disable_self", "不能封禁当前登录的超级管理员")
		return
	}
	var request struct {
		Status uint8 `json:"status"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Status > 1 {
		writeError(w, http.StatusBadRequest, "invalid_status", "用户状态只能是 0 或 1")
		return
	}
	result, err := s.db.ExecContext(r.Context(), `UPDATE users SET status = ? WHERE id = ?`, request.Status, userID)
	if err != nil {
		s.internalError(w, "update user status failed", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "user_not_found", "用户不存在")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminUserPassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := requestedUserID(w, r)
	if !ok {
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Password) < 8 || len(request.Password) > 72 || strings.TrimSpace(request.Password) == "" {
		writeError(w, http.StatusBadRequest, "invalid_password", "密码长度须为 8–72 位")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		s.internalError(w, "password hashing failed", err)
		return
	}
	result, err := s.db.ExecContext(
		r.Context(),
		`UPDATE users SET password_hash = ? WHERE id = ?`,
		string(hash), userID,
	)
	if err != nil {
		s.internalError(w, "update password failed", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		var exists int
		err = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "user_not_found", "用户不存在")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
