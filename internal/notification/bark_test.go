package notification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBarkSenderPush(t *testing.T) {
	t.Parallel()

	var received barkPushRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/push" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
	}))
	defer server.Close()

	sender := newBarkSender(server.URL)
	if err := sender.push(context.Background(), "device-key-123", "body", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if received.DeviceKey != "device-key-123" || received.Body != "body" || received.Group != "AutoBuff" {
		t.Fatalf("unexpected Bark request: %#v", received)
	}
	if received.Level != "active" || received.Volume != nil || received.URL != "https://example.com" {
		t.Fatalf("normal push options changed unexpectedly: %#v", received)
	}
	if _, err := time.ParseInLocation("2006-01-02 15:04:05", received.Title, time.Local); err != nil {
		t.Fatalf("expected current time as Bark title, got %q", received.Title)
	}
}

func TestBarkSenderRejectsErrorResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusTooManyRequests)
	}))
	defer server.Close()

	if err := newBarkSender(server.URL).push(context.Background(), "device-key-123", "body", ""); err == nil {
		t.Fatal("expected Bark error response")
	}
}

func TestBarkSenderPushesCriticalAlertAtMaximumVolume(t *testing.T) {
	t.Parallel()

	var received barkPushRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
	}))
	defer server.Close()

	if err := newBarkSender(server.URL).pushCritical(
		context.Background(), "device-key-123", "重要警告", 7,
	); err != nil {
		t.Fatal(err)
	}
	if received.Level != "critical" || received.Volume == nil || *received.Volume != 7 || received.Body != "重要警告" {
		t.Fatalf("unexpected critical Bark request: %#v", received)
	}
}

func TestBarkSenderPreservesZeroCriticalVolume(t *testing.T) {
	t.Parallel()

	var received barkPushRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200})
	}))
	defer server.Close()

	if err := newBarkSender(server.URL).pushCritical(
		context.Background(), "device-key-123", "重要警告", 0,
	); err != nil {
		t.Fatal(err)
	}
	if received.Volume == nil || *received.Volume != 0 {
		t.Fatalf("expected explicit zero volume, got %#v", received.Volume)
	}
}

func TestBarkSenderCriticalAlertPreservesTargetURL(t *testing.T) {
	t.Parallel()

	var received barkPushRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200})
	}))
	defer server.Close()

	if err := newBarkSender(server.URL).pushCriticalWithURL(
		context.Background(),
		"device-key-123",
		"出现紫色符文，尽快解除！",
		"https://example.com/dashboard",
		4,
	); err != nil {
		t.Fatal(err)
	}
	if received.Level != "critical" || received.Volume == nil || *received.Volume != 4 {
		t.Fatalf("expected audible critical alert, got %#v", received)
	}
	if received.URL != "https://example.com/dashboard" {
		t.Fatalf("expected dashboard target URL, got %q", received.URL)
	}
}

func TestBarkSenderVerifiesRegisteredDeviceKey(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/register/device-key-123" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success"})
	}))
	defer server.Close()

	if err := newBarkSender(server.URL).verifyDeviceKey(context.Background(), "device-key-123"); err != nil {
		t.Fatal(err)
	}
}

func TestBarkSenderRejectsUnregisteredDeviceKey(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 400, "message": "key not found"})
	}))
	defer server.Close()

	if err := newBarkSender(server.URL).verifyDeviceKey(context.Background(), "missing-key"); err == nil {
		t.Fatal("expected unregistered DeviceKey error")
	}
}
