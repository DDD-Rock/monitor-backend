package api

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

type saveBarkRequest struct {
	DeviceKey string `json:"deviceKey"`
}

type toggleEXPStalledRequest struct {
	Enabled bool `json:"enabled"`
	Seconds int  `json:"seconds"`
}

type toggleRuneAlertRequest struct {
	Enabled bool `json:"enabled"`
}

type toggleZoneBreachRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleBarkSettings(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	settings, err := s.notify.Settings(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "query Bark settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleSaveBark(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request saveBarkRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SaveBark(r.Context(), user.ID, request.DeviceKey); err != nil {
		if strings.Contains(err.Error(), "invalid Bark") {
			writeError(w, http.StatusBadRequest, "invalid_device_key", "Bark DeviceKey 格式无效")
			return
		}
		if strings.Contains(err.Error(), "未注册") {
			writeError(w, http.StatusBadRequest, "unregistered_device_key", "该 DeviceKey 未在自建 Bark 服务器注册，请复制 Bark 推送地址最后一段")
			return
		}
		s.internalError(w, "save Bark settings failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "query saved Bark settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleBarkTest(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	s.sendBarkTest(w, r, user.ID)
}

func (s *Server) handlePreviewBarkSettings(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.previewUserID(w, r)
	if !ok {
		return
	}
	settings, err := s.notify.Settings(r.Context(), userID)
	if err != nil {
		s.internalError(w, "query preview Bark settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handlePreviewEXPStalled(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.previewUserID(w, r)
	if !ok {
		return
	}
	var request toggleEXPStalledRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetEXPStalled(r.Context(), userID, request.Enabled, request.Seconds); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		if strings.Contains(err.Error(), "秒数") {
			writeError(w, http.StatusBadRequest, "invalid_stall_seconds", err.Error())
			return
		}
		s.internalError(w, "update preview EXP stalled notification failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), userID)
	if err != nil {
		s.internalError(w, "query updated EXP stalled settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handlePreviewRuneAlert(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.previewUserID(w, r)
	if !ok {
		return
	}
	var request toggleRuneAlertRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetRuneAlertEnabled(r.Context(), userID, request.Enabled); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		s.internalError(w, "update preview rune alert failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), userID)
	if err != nil {
		s.internalError(w, "query updated rune alert settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handlePreviewZoneBreach(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.previewUserID(w, r)
	if !ok {
		return
	}
	var request toggleZoneBreachRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetZoneBreachEnabled(r.Context(), userID, request.Enabled); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		s.internalError(w, "update preview zone breach failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), userID)
	if err != nil {
		s.internalError(w, "query updated zone breach settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handlePreviewBarkTest(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.previewUserID(w, r)
	if !ok {
		return
	}
	s.sendBarkTest(w, r, userID)
}

func (s *Server) sendBarkTest(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := s.notify.SendTest(r.Context(), userID); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		if strings.Contains(err.Error(), "频繁") {
			writeError(w, http.StatusTooManyRequests, "test_rate_limited", err.Error())
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "device token") ||
			strings.Contains(strings.ToLower(err.Error()), "device key") {
			writeError(w, http.StatusConflict, "invalid_bark_device_key", "当前保存的不是有效 Bark DeviceKey，请重新复制 Bark 推送地址最后一段")
			return
		}
		s.logger.Warn("send Bark test failed", "user_id", userID, "error", err)
		writeError(w, http.StatusBadGateway, "bark_push_failed", "Bark 推送失败，请检查 DeviceKey 和服务器")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func (s *Server) previewUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	rawToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if rawToken == "" {
		writeError(w, http.StatusUnauthorized, "missing_preview_token", "预览链接无效")
		return 0, false
	}
	tokenHash := sha256.Sum256([]byte(rawToken))
	var userID int64
	if err := s.db.QueryRowContext(
		r.Context(),
		`SELECT user_id FROM monitor_sessions
		 WHERE preview_token_hash = ? AND status = 1 LIMIT 1`,
		tokenHash[:],
	).Scan(&userID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.logger.Error("query preview notification owner failed", "error", err)
		}
		writeError(w, http.StatusUnauthorized, "invalid_preview_token", "预览链接无效或已失效")
		return 0, false
	}
	return userID, true
}
