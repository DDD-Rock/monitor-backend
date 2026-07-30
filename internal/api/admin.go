package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type adminUserItem struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	Status         uint8  `json:"status"`
	IsSuperAdmin   bool   `json:"isSuperAdmin"`
	CreatedAt      int64  `json:"createdAt"`
	LastLoginAt    *int64 `json:"lastLoginAt"`
	ConnectedCount int    `json:"connectedClientCount"`
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(
		r.Context(),
		`SELECT u.id, u.username, u.status, u.is_super_admin, u.created_at, u.last_login_at,
		        COUNT(CASE WHEN ms.status = 1 THEN 1 END)
		 FROM users u
		 LEFT JOIN monitor_sessions ms ON ms.user_id = u.id
		 GROUP BY u.id, u.username, u.status, u.is_super_admin, u.created_at, u.last_login_at
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
		var createdAt time.Time
		var lastLogin sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.Username, &item.Status, &isSuperAdmin,
			&createdAt, &lastLogin, &item.ConnectedCount,
		); err != nil {
			s.internalError(w, "scan users failed", err)
			return
		}
		item.IsSuperAdmin = isSuperAdmin == 1
		item.CreatedAt = createdAt.UnixMilli()
		if lastLogin.Valid {
			value := lastLogin.Time.UnixMilli()
			item.LastLoginAt = &value
		}
		users = append(users, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
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
