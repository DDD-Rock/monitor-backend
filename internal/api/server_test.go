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
		{http.MethodPut, "/api/notifications/mouse-follow-verification", `{"enabled":true}`},
		{http.MethodPut, "/api/notifications/urgent-mute", `{"muted":true}`},
		{http.MethodPut, "/api/notifications/zone-breach", `{"enabled":true}`},
		{http.MethodGet, "/api/monitor/exp-gain", ""},
		{http.MethodPost, "/api/monitor/exp-gain/reset-total", ""},
		{http.MethodPost, "/api/clients/bind", `{"clientId":"test-client-id"}`},
		{http.MethodGet, "/api/clients/authorization?client_id=test-client-id", ""},
		{http.MethodDelete, "/api/clients/00000000-0000-0000-0000-000000000000", ""},
		{http.MethodGet, "/api/rope-team", ""},
		{http.MethodPut, "/api/rope-team", `{"leaderSessionId":"00000000-0000-0000-0000-000000000000","memberSessionIds":[]}`},
		{http.MethodPut, "/api/rope-team/boss", `{"roleName":"老板"}`},
		{http.MethodDelete, "/api/rope-team/members/00000000-0000-0000-0000-000000000000", ""},
		{http.MethodDelete, "/api/rope-team", ""},
		{http.MethodGet, "/api/admin/users/1/clients", ""},
		{http.MethodDelete, "/api/admin/users/1/clients/00000000-0000-0000-0000-000000000000", ""},
		{http.MethodPatch, "/api/admin/users/1/client-limit", `{"maxClientCount":1}`},
		{http.MethodGet, "/api/admin/invite-codes", ""},
		{http.MethodPost, "/api/admin/invite-codes", `{"durationSeconds":1800}`},
		{http.MethodDelete, "/api/admin/invite-codes/1", ""},
		{http.MethodGet, "/api/admin/client-versions", ""},
		{http.MethodPut, "/api/admin/client-versions", `{"platform":"macos","version":"2.0.1","enabled":false}`},
		{http.MethodGet, "/api/admin/maps", ""},
		{http.MethodPost, "/api/admin/maps", `{"formatVersion":1,"maps":[]}`},
		{http.MethodGet, "/api/admin/maps/1", ""},
		{http.MethodDelete, "/api/admin/maps/1", ""},
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

func TestNormalizeInviteCode(t *testing.T) {
	t.Parallel()

	if actual := normalizeInviteCode("  abcd-1234  "); actual != "ABCD-1234" {
		t.Fatalf("邀请码规范化结果错误: %q", actual)
	}
	if actual := normalizeInviteCode("  "); actual != "" {
		t.Fatalf("空邀请码规范化结果错误: %q", actual)
	}
}

func TestValidInviteCode(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"ABC123", "000000", "ZZZZZZ"} {
		if !validInviteCode(value) {
			t.Fatalf("六位字母数字邀请码应有效: %q", value)
		}
	}
	for _, value := range []string{"AB-123", "ABCDE", "ABCDEFG", "abcdef", ""} {
		if validInviteCode(value) {
			t.Fatalf("旧格式或非六位邀请码应无效: %q", value)
		}
	}
}

func TestNewInviteCode(t *testing.T) {
	t.Parallel()
	first, err := newInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 6 || !validInviteCode(first) || first == second {
		t.Fatalf("生成的邀请码格式或随机性异常: %q %q", first, second)
	}
}

func TestNormalizeClientVersion(t *testing.T) {
	t.Parallel()
	platform, version, ok := normalizeClientVersion(" macOS ", "2.0.1-beta+1")
	if !ok || platform != "macos" || version != "2.0.1-beta+1" {
		t.Fatalf("客户端版本规范化失败: %q %q %v", platform, version, ok)
	}
	for _, item := range [][2]string{{"linux", "2.0.1"}, {"windows", ""}, {"windows", "2.0.1/invalid"}} {
		if _, _, valid := normalizeClientVersion(item[0], item[1]); valid {
			t.Fatalf("无效客户端版本不应通过: %q %q", item[0], item[1])
		}
	}
}

func TestValidNickname(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"小新", "Alice", "地图 管理员", "🙂"} {
		if !validNickname(value) {
			t.Fatalf("expected nickname %q to be valid", value)
		}
	}
	for _, value := range []string{"", "1234567890123456789012345", "名字\n换行"} {
		if validNickname(value) {
			t.Fatalf("expected nickname %q to be invalid", value)
		}
	}
}
