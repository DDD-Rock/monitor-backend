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

func TestAccountMonitorRoutesRequireLogin(t *testing.T) {
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

	// 没有账号登录令牌时应在触碰数据库之前返回 401；
	// 如果路由没有注册，得到的会是 404。
	for _, item := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPut, "/api/notifications/exp-stalled", `{"enabled":true}`},
		{http.MethodPut, "/api/notifications/rune-alert", `{"enabled":true}`},
		{http.MethodPut, "/api/notifications/zone-breach", `{"enabled":true}`},
		{http.MethodGet, "/api/monitor/exp-gain", ""},
		{http.MethodPost, "/api/monitor/exp-gain/reset-total", ""},
		{http.MethodPost, "/api/clients/bind", `{"clientId":"test-client-id"}`},
		{http.MethodGet, "/api/clients/authorization?client_id=test-client-id", ""},
		{http.MethodGet, "/api/admin/users/1/clients", ""},
		{http.MethodDelete, "/api/admin/users/1/clients/00000000-0000-0000-0000-000000000000", ""},
		{http.MethodPatch, "/api/admin/users/1/client-limit", `{"maxClientCount":1}`},
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

func TestPreviewTokenRoutesAreGone(t *testing.T) {
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
		http.MethodGet,
		"/api/preview/notifications/bark?token=legacy",
		nil,
	)
	recorder := httptest.NewRecorder()
	server.Routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("旧预览 Key 接口应返回 404，实际 %d", recorder.Code)
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
