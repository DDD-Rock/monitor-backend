package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type barkSender struct {
	baseURL string
	client  *http.Client
}

type barkPushRequest struct {
	DeviceKey string `json:"device_key"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body"`
	Group     string `json:"group,omitempty"`
	Level     string `json:"level,omitempty"`
	Volume    *int   `json:"volume,omitempty"`
	URL       string `json:"url,omitempty"`
}

type barkPushResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newBarkSender(baseURL string) *barkSender {
	return &barkSender{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

func (s *barkSender) push(ctx context.Context, deviceKey, body, targetURL string) error {
	return s.pushWithOptions(ctx, deviceKey, body, targetURL, "active", nil)
}

func (s *barkSender) pushCritical(
	ctx context.Context,
	deviceKey string,
	body string,
	volume int,
) error {
	return s.pushCriticalWithURL(ctx, deviceKey, body, "", volume)
}

func (s *barkSender) pushCriticalWithURL(
	ctx context.Context,
	deviceKey string,
	body string,
	targetURL string,
	volume int,
) error {
	return s.pushWithOptions(ctx, deviceKey, body, targetURL, "critical", &volume)
}

func (s *barkSender) pushWithOptions(
	ctx context.Context,
	deviceKey string,
	body string,
	targetURL string,
	level string,
	volume *int,
) error {
	payload, err := json.Marshal(barkPushRequest{
		DeviceKey: deviceKey,
		Title:     time.Now().Format("2006-01-02 15:04:05"),
		Body:      body,
		Group:     "AutoBuff",
		Level:     level,
		Volume:    volume,
		URL:       targetURL,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/push", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("request Bark: %w", err)
	}
	defer response.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var result barkPushResponse
		if json.Unmarshal(bodyBytes, &result) == nil && result.Message != "" {
			return fmt.Errorf("Bark returned HTTP %d: %s", response.StatusCode, result.Message)
		}
		return fmt.Errorf("Bark returned HTTP %d", response.StatusCode)
	}
	var result barkPushResponse
	if len(bodyBytes) > 0 && json.Unmarshal(bodyBytes, &result) == nil && result.Code != 0 && result.Code != 200 {
		return fmt.Errorf("Bark rejected push: code=%d message=%s", result.Code, result.Message)
	}
	return nil
}

func (s *barkSender) verifyDeviceKey(ctx context.Context, deviceKey string) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		s.baseURL+"/register/"+url.PathEscape(deviceKey),
		nil,
	)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("request Bark: %w", err)
	}
	defer response.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, 16*1024))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	var result barkPushResponse
	if json.Unmarshal(bodyBytes, &result) == nil && result.Message != "" {
		return fmt.Errorf("Bark DeviceKey 未注册: %s", result.Message)
	}
	return fmt.Errorf("Bark DeviceKey 未注册")
}
