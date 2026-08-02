package api

import (
	"net/http"
	"strings"
	"time"
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

type toggleMouseFollowVerificationRequest struct {
	Enabled bool `json:"enabled"`
}

type toggleUrgentMuteRequest struct {
	Muted bool `json:"muted"`
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
	s.sendBarkTest(w, r, user.ID, nil)
}

func (s *Server) handleBarkCriticalTest(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request struct {
		Volume *int `json:"volume"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.Volume == nil || *request.Volume < 0 || *request.Volume > 10 {
		writeError(w, http.StatusBadRequest, "invalid_critical_volume", "紧急通知音量须为 0–10")
		return
	}
	s.sendBarkTest(w, r, user.ID, request.Volume)
}

func (s *Server) handleEXPStalled(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request toggleEXPStalledRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetEXPStalled(r.Context(), user.ID, request.Enabled, request.Seconds); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		if strings.Contains(err.Error(), "秒数") {
			writeError(w, http.StatusBadRequest, "invalid_stall_seconds", err.Error())
			return
		}
		s.internalError(w, "update EXP stalled notification failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "query updated EXP stalled settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleRuneAlert(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request toggleRuneAlertRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetRuneAlertEnabled(r.Context(), user.ID, request.Enabled); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		s.internalError(w, "update rune alert failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "query updated rune alert settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleMouseFollowVerification(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request toggleMouseFollowVerificationRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetMouseFollowVerificationEnabled(
		r.Context(),
		user.ID,
		request.Enabled,
	); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		s.internalError(w, "update mouse follow verification alert failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "query updated mouse follow verification settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUrgentMute(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request toggleUrgentMuteRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetUrgentAlertsMuted(r.Context(), user.ID, request.Muted); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		s.internalError(w, "update urgent alert mute setting failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "query updated urgent alert mute setting failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleZoneBreach(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	var request toggleZoneBreachRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if err := s.notify.SetZoneBreachEnabled(r.Context(), user.ID, request.Enabled); err != nil {
		if strings.Contains(err.Error(), "尚未配置") {
			writeError(w, http.StatusConflict, "bark_not_configured", err.Error())
			return
		}
		s.internalError(w, "update zone breach failed", err)
		return
	}
	settings, err := s.notify.Settings(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "query updated zone breach settings failed", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) sendBarkTest(
	w http.ResponseWriter,
	r *http.Request,
	userID int64,
	criticalVolume *int,
) {
	var err error
	if criticalVolume != nil {
		err = s.notify.SendCriticalTest(r.Context(), userID, *criticalVolume)
	} else {
		err = s.notify.SendTest(r.Context(), userID)
	}
	if err != nil {
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

func (s *Server) handleEXPGain(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	if s.expGain == nil {
		writeError(w, http.StatusServiceUnavailable, "exp_gain_unavailable", "经验累计服务未就绪")
		return
	}
	writeJSON(w, http.StatusOK, s.expGain.Snapshot(user.ID, time.Now()))
}

func (s *Server) handleResetEXPGainTotal(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r.Context())
	if s.expGain == nil {
		writeError(w, http.StatusServiceUnavailable, "exp_gain_unavailable", "经验累计服务未就绪")
		return
	}
	payload, err := s.expGain.ResetTotal(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "reset exp gain total failed", err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}
