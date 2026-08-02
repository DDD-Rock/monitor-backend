package api

import (
	"net/http"
	"regexp"
	"strings"
)

const (
	clientPlatformHeader = "X-AutoBuff-Client-Platform"
	clientVersionHeader  = "X-AutoBuff-Client-Version"
)

var clientVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,31}$`)

type clientVersionPolicy struct {
	Platform  string `json:"platform"`
	Version   string `json:"version"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

func normalizeClientVersion(platform, version string) (string, string, bool) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	version = strings.TrimSpace(version)
	validPlatform := platform == "macos" || platform == "windows"
	return platform, version, validPlatform && clientVersionPattern.MatchString(version)
}

// Requests without client headers are browser/API logins and are not governed
// by desktop client version policies.
func (s *Server) requireAllowedClientVersion(w http.ResponseWriter, r *http.Request) bool {
	rawPlatform := r.Header.Get(clientPlatformHeader)
	rawVersion := r.Header.Get(clientVersionHeader)
	if strings.TrimSpace(rawPlatform) == "" && strings.TrimSpace(rawVersion) == "" {
		return true
	}
	platform, version, valid := normalizeClientVersion(rawPlatform, rawVersion)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_client_version", "客户端版本信息无效，请更新客户端后重试")
		return false
	}
	if _, err := s.db.ExecContext(
		r.Context(),
		`INSERT IGNORE INTO client_version_policies (platform, version, enabled) VALUES (?, ?, 1)`,
		platform,
		version,
	); err != nil {
		s.internalError(w, "register client version failed", err)
		return false
	}
	var enabled uint8
	if err := s.db.QueryRowContext(
		r.Context(),
		`SELECT enabled FROM client_version_policies WHERE platform = ? AND version = ?`,
		platform,
		version,
	).Scan(&enabled); err != nil {
		s.internalError(w, "check client version failed", err)
		return false
	}
	if enabled != 1 {
		writeError(
			w,
			http.StatusForbidden,
			"client_version_disabled",
			"当前客户端版本已停用，请更新到最新版本后再登录",
		)
		return false
	}
	return true
}

func (s *Server) handleAdminClientVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(
		r.Context(),
		`SELECT platform, version, enabled,
		        CAST(UNIX_TIMESTAMP(created_at) * 1000 AS SIGNED),
		        CAST(UNIX_TIMESTAMP(updated_at) * 1000 AS SIGNED)
		   FROM client_version_policies
		  ORDER BY platform, created_at DESC, version DESC`,
	)
	if err != nil {
		s.internalError(w, "list client versions failed", err)
		return
	}
	defer rows.Close()

	items := make([]clientVersionPolicy, 0)
	for rows.Next() {
		var item clientVersionPolicy
		var enabled uint8
		if err := rows.Scan(
			&item.Platform,
			&item.Version,
			&enabled,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			s.internalError(w, "scan client version failed", err)
			return
		}
		item.Enabled = enabled == 1
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, "iterate client versions failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": items})
}

func (s *Server) handleSaveAdminClientVersion(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Platform string `json:"platform"`
		Version  string `json:"version"`
		Enabled  *bool  `json:"enabled"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	platform, version, valid := normalizeClientVersion(request.Platform, request.Version)
	if !valid || request.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_client_version", "请选择客户端平台并填写有效版本号")
		return
	}
	enabled := uint8(0)
	if *request.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(
		r.Context(),
		`INSERT INTO client_version_policies (platform, version, enabled)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), updated_at = CURRENT_TIMESTAMP(3)`,
		platform,
		version,
		enabled,
	)
	if err != nil {
		s.internalError(w, "save client version failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platform": platform,
		"version":  version,
		"enabled":  *request.Enabled,
	})
}
