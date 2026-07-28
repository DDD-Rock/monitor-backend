package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"autobuff-monitor/server/internal/config"
)

func TestPreviewNotificationRoutesAreRegistered(t *testing.T) {
	t.Parallel()

	server := NewServer(
		config.Config{},
		nil,
		nil,
		nil,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	routes := server.Routes()

	// 没带预览 token 时应在触碰数据库之前就返回 401；
	// 如果路由没注册，得到的会是 404。
	for _, item := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/preview/notifications/exp-stalled", `{"enabled":true}`},
		{http.MethodPut, "/api/preview/notifications/rune-alert", `{"enabled":true}`},
		{http.MethodPut, "/api/preview/notifications/zone-breach", `{"enabled":true}`},
		{http.MethodGet, "/api/preview/exp-gain", ""},
		{http.MethodPost, "/api/preview/exp-gain/reset-total", ""},
	} {
		request := httptest.NewRequest(item.method, item.path, strings.NewReader(item.body))
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s 应返回 401，实际 %d", item.method, item.path, recorder.Code)
		}
	}
}

func TestRemovedEXPMinuteRouteIsGone(t *testing.T) {
	t.Parallel()

	server := NewServer(
		config.Config{},
		nil,
		nil,
		nil,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	request := httptest.NewRequest(
		http.MethodPut,
		"/api/preview/notifications/exp-minute",
		strings.NewReader(`{"enabled":true}`),
	)
	recorder := httptest.NewRecorder()
	server.Routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("每分钟经验推送已下线，应返回 404，实际 %d", recorder.Code)
	}
}

func TestValidRegistrationInviteCode(t *testing.T) {
	t.Parallel()

	valid := []string{
		"XIAOXIN",
		"xiaoxin",
		"XiaoXin",
		"  xIaOxIn  ",
	}
	for _, value := range valid {
		if !validRegistrationInviteCode(value) {
			t.Fatalf("expected invite code %q to be valid", value)
		}
	}

	invalid := []string{"", "XIAOXIN1", "XIAO XIN", "OTHER"}
	for _, value := range invalid {
		if validRegistrationInviteCode(value) {
			t.Fatalf("expected invite code %q to be invalid", value)
		}
	}
}
