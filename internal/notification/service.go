package notification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"autobuff-monitor/server/internal/protocol"
	"autobuff-monitor/server/internal/realtime"

	"github.com/go-sql-driver/mysql"
)

const (
	ProviderBark                = "bark"
	RuleEXPStalled              = "exp_stalled"
	RuleRuneAlert               = "rune_alert"
	RuleMouseFollowVerification = "mouse_follow_verification"
	RuleZoneBreach              = "zone_breach"
	DefaultStallSeconds         = 120
	MinStallSeconds             = 10
	MaxStallSeconds             = 86400
	// 符文提示仍在画面上时，每 5 秒重复提醒一次。
	RuneAlertIntervalSeconds = 5
	// Mac 端每 3 秒心跳一次符文状态；超过这个时间没更新就当作数据过期，
	// 避免客户端掉线后按最后一次上报无限推送。
	RuneAlertFreshnessWindow = 12 * time.Second
	// 鼠标跟随验证需要立即人工介入；每 2 秒重复提醒，服务端每秒扫描一次。
	MouseFollowVerificationIntervalSeconds = 2
	MouseFollowVerificationFreshnessWindow = 12 * time.Second
	// 角色仍在安全区外时，每 5 秒重复报警一次。
	ZoneBreachIntervalSeconds = 5
	// 与符文同理：Mac 端心跳停止后，最后一次「已越界」的上报很快过期。
	ZoneBreachFreshnessWindow = 12 * time.Second
	// 调度器每 5 秒扫一次，而 last_sent_at 记录的是这一轮推送完成的时刻，
	// 总是比 tick 晚若干毫秒。不留容差的话每条规则都会顺延整整一个周期
	// （5 秒变 10 秒、60 秒变 65 秒），因此允许提前 1 秒触发。
	scheduleSlack = time.Second
	// 调度器扫描周期，容差必须小于它，否则同一周期内会重复推送。
	scheduleInterval = 5 * time.Second
)

var barkDeviceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

type Settings struct {
	Configured                             bool   `json:"configured"`
	UrgentAlertsMuted                      bool   `json:"urgentAlertsMuted"`
	EXPStalledEnabled                      bool   `json:"expStalledEnabled"`
	EXPStalledSeconds                      int    `json:"expStalledSeconds"`
	RuneAlertEnabled                       bool   `json:"runeAlertEnabled"`
	RuneAlertIntervalSeconds               int    `json:"runeAlertIntervalSeconds"`
	MouseFollowVerificationEnabled         bool   `json:"mouseFollowVerificationEnabled"`
	MouseFollowVerificationIntervalSeconds int    `json:"mouseFollowVerificationIntervalSeconds"`
	ZoneBreachEnabled                      bool   `json:"zoneBreachEnabled"`
	ZoneBreachIntervalSeconds              int    `json:"zoneBreachIntervalSeconds"`
	BarkServerURL                          string `json:"barkServerURL"`
}

type Service struct {
	db            *sql.DB
	hub           *realtime.Hub
	box           *secretBox
	bark          *barkSender
	publicBaseURL string
	publicBarkURL string
	logger        *slog.Logger
}

func NewService(
	db *sql.DB,
	hub *realtime.Hub,
	secretMaterial []byte,
	barkBaseURL string,
	publicBarkURL string,
	publicBaseURL string,
	logger *slog.Logger,
) (*Service, error) {
	box, err := newSecretBox(secretMaterial)
	if err != nil {
		return nil, err
	}
	return &Service{
		db:            db,
		hub:           hub,
		box:           box,
		bark:          newBarkSender(barkBaseURL),
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		publicBarkURL: strings.TrimRight(publicBarkURL, "/"),
		logger:        logger,
	}, nil
}

func (s *Service) Settings(ctx context.Context, userID int64) (Settings, error) {
	settings := Settings{
		BarkServerURL:                          s.publicBarkURL,
		EXPStalledSeconds:                      DefaultStallSeconds,
		RuneAlertIntervalSeconds:               RuneAlertIntervalSeconds,
		MouseFollowVerificationIntervalSeconds: MouseFollowVerificationIntervalSeconds,
		ZoneBreachIntervalSeconds:              ZoneBreachIntervalSeconds,
	}
	var channelEnabled bool
	err := s.db.QueryRowContext(
		ctx,
		`SELECT enabled, urgent_alerts_muted
		 FROM notification_channels WHERE user_id = ? AND provider = ? LIMIT 1`,
		userID,
		ProviderBark,
	).Scan(&channelEnabled, &settings.UrgentAlertsMuted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Settings{}, err
	}
	settings.Configured = err == nil && channelEnabled

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT rule_key, enabled, interval_seconds FROM notification_rules
		 WHERE user_id = ? AND rule_key IN (?, ?, ?, ?)`,
		userID,
		RuleEXPStalled,
		RuleRuneAlert,
		RuleMouseFollowVerification,
		RuleZoneBreach,
	)
	if err != nil {
		return Settings{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var ruleKey string
		var enabled bool
		var intervalSeconds int
		if err := rows.Scan(&ruleKey, &enabled, &intervalSeconds); err != nil {
			return Settings{}, err
		}
		switch ruleKey {
		case RuleEXPStalled:
			settings.EXPStalledEnabled = enabled
			settings.EXPStalledSeconds = normalizeStallSeconds(intervalSeconds)
		case RuleRuneAlert:
			settings.RuneAlertEnabled = enabled
		case RuleMouseFollowVerification:
			settings.MouseFollowVerificationEnabled = enabled
		case RuleZoneBreach:
			settings.ZoneBreachEnabled = enabled
		}
	}
	return settings, nil
}

func (s *Service) SaveBark(ctx context.Context, userID int64, deviceKey string) error {
	deviceKey = strings.TrimSpace(deviceKey)
	if !barkDeviceKeyPattern.MatchString(deviceKey) {
		return fmt.Errorf("invalid Bark DeviceKey")
	}
	if err := s.bark.verifyDeviceKey(ctx, deviceKey); err != nil {
		return err
	}
	encrypted, err := s.box.seal(deviceKey)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO notification_channels (user_id, provider, secret_ciphertext, enabled)
		 VALUES (?, ?, ?, 1)
		 ON DUPLICATE KEY UPDATE secret_ciphertext = VALUES(secret_ciphertext), enabled = 1`,
		userID,
		ProviderBark,
		encrypted,
	); err != nil {
		return err
	}
	// 鼠标跟随验证属于必须尽快人工介入的安全告警；绑定 Bark 后默认开启，
	// 用户仍可在监控页单独关闭。
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO notification_rules (user_id, rule_key, enabled, interval_seconds)
		 VALUES (?, ?, 1, ?)
		 ON DUPLICATE KEY UPDATE interval_seconds = VALUES(interval_seconds)`,
		userID,
		RuleMouseFollowVerification,
		MouseFollowVerificationIntervalSeconds,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) SetEXPStalled(ctx context.Context, userID int64, enabled bool, seconds int) error {
	if seconds < MinStallSeconds || seconds > MaxStallSeconds {
		return fmt.Errorf("经验停滞秒数必须在 %d–%d 之间", MinStallSeconds, MaxStallSeconds)
	}
	if enabled && !s.barkConfigured(ctx, userID) {
		return fmt.Errorf("Bark DeviceKey 尚未配置")
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO notification_rules (user_id, rule_key, enabled, interval_seconds)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), interval_seconds = VALUES(interval_seconds),
		 last_sent_at = IF(VALUES(enabled) = 1, NULL, last_sent_at)`,
		userID,
		RuleEXPStalled,
		enabled,
		seconds,
	)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		`DELETE FROM notification_rule_states WHERE user_id = ? AND rule_key = ?`,
		userID,
		RuleEXPStalled,
	)
	return err
}

// SetRuneAlertEnabled 控制「符文提示推送」这个独立开关。
// 间隔固定为 5 秒，不开放给用户调节，避免误设成刷屏或形同虚设的数值。
func (s *Service) SetRuneAlertEnabled(ctx context.Context, userID int64, enabled bool) error {
	if enabled && !s.barkConfigured(ctx, userID) {
		return fmt.Errorf("Bark DeviceKey 尚未配置")
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO notification_rules (user_id, rule_key, enabled, interval_seconds)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), interval_seconds = VALUES(interval_seconds),
		 last_sent_at = IF(VALUES(enabled) = 1, NULL, last_sent_at)`,
		userID,
		RuleRuneAlert,
		enabled,
		RuneAlertIntervalSeconds,
	)
	return err
}

// SetMouseFollowVerificationEnabled 控制「鼠标跟随验证紧急推送」独立开关。
func (s *Service) SetMouseFollowVerificationEnabled(
	ctx context.Context,
	userID int64,
	enabled bool,
) error {
	if enabled && !s.barkConfigured(ctx, userID) {
		return fmt.Errorf("Bark DeviceKey 尚未配置")
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO notification_rules (user_id, rule_key, enabled, interval_seconds)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), interval_seconds = VALUES(interval_seconds),
		 last_sent_at = IF(VALUES(enabled) = 1, NULL, last_sent_at)`,
		userID,
		RuleMouseFollowVerification,
		enabled,
		MouseFollowVerificationIntervalSeconds,
	)
	return err
}

// SetUrgentAlertsMuted controls the sound of the first rune/mouse-follow alert
// in each active event cycle. Repeated critical alerts are always silent.
func (s *Service) SetUrgentAlertsMuted(ctx context.Context, userID int64, muted bool) error {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE notification_channels SET urgent_alerts_muted = ?
		 WHERE user_id = ? AND provider = ? AND enabled = 1`,
		muted,
		userID,
		ProviderBark,
	)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 && !s.barkConfigured(ctx, userID) {
		return fmt.Errorf("Bark DeviceKey 尚未配置")
	}
	return nil
}

// SetZoneBreachEnabled 控制「离开安全区报警」这个独立开关。
// 与符文一致，间隔固定为 5 秒，不开放调节。
func (s *Service) SetZoneBreachEnabled(ctx context.Context, userID int64, enabled bool) error {
	if enabled && !s.barkConfigured(ctx, userID) {
		return fmt.Errorf("Bark DeviceKey 尚未配置")
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO notification_rules (user_id, rule_key, enabled, interval_seconds)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled = VALUES(enabled), interval_seconds = VALUES(interval_seconds),
		 last_sent_at = IF(VALUES(enabled) = 1, NULL, last_sent_at)`,
		userID,
		RuleZoneBreach,
		enabled,
		ZoneBreachIntervalSeconds,
	)
	return err
}

func (s *Service) barkConfigured(ctx context.Context, userID int64) bool {
	var exists int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT 1 FROM notification_channels
		 WHERE user_id = ? AND provider = ? AND enabled = 1 LIMIT 1`,
		userID,
		ProviderBark,
	).Scan(&exists)
	return err == nil
}

func (s *Service) SendTest(ctx context.Context, userID int64) error {
	return s.sendTest(ctx, userID, "test", false, 0)
}

func (s *Service) SendCriticalTest(ctx context.Context, userID int64, volume int) error {
	if volume < 0 || volume > 10 {
		return fmt.Errorf("紧急通知音量须为 0–10")
	}
	return s.sendTest(ctx, userID, "critical_test", true, volume)
}

func (s *Service) sendTest(
	ctx context.Context,
	userID int64,
	eventType string,
	critical bool,
	criticalVolume int,
) error {
	var lastAttempt sql.NullTime
	_ = s.db.QueryRowContext(
		ctx,
		`SELECT MAX(created_at) FROM notification_deliveries
		 WHERE user_id = ? AND provider = ? AND event_type = ?`,
		userID,
		ProviderBark,
		eventType,
	).Scan(&lastAttempt)
	if lastAttempt.Valid && time.Since(lastAttempt.Time) < 10*time.Second {
		return fmt.Errorf("测试通知发送过于频繁，请稍后再试")
	}

	deviceKey, err := s.deviceKey(ctx, userID)
	if err != nil {
		return err
	}
	if critical {
		err = s.bark.pushCritical(ctx, deviceKey, "重要警告", criticalVolume)
	} else {
		err = s.bark.push(ctx, deviceKey, "出现紫色符文，尽快解除！", s.publicBaseURL)
	}
	s.recordDelivery(ctx, userID, eventType, err)
	return err
}

func (s *Service) deviceKey(ctx context.Context, userID int64) (string, error) {
	var ciphertext string
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT secret_ciphertext FROM notification_channels
		 WHERE user_id = ? AND provider = ? AND enabled = 1 LIMIT 1`,
		userID,
		ProviderBark,
	).Scan(&ciphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("Bark DeviceKey 尚未配置")
		}
		return "", err
	}
	return s.box.open(ciphertext)
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(scheduleInterval)
		urgentTicker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer urgentTicker.Stop()
		for {
			select {
			case <-ticker.C:
				s.processEXPStalls(ctx)
				s.processDueZoneBreaches(ctx)
			case <-urgentTicker.C:
				s.processDueRuneAlerts(ctx)
				s.processDueMouseFollowVerifications(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

type stalledEXPRule struct {
	UserID           int64
	SessionID        string
	SecretCiphertext string
	ThresholdSeconds int
}

func (s *Service) processEXPStalls(ctx context.Context) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT nr.user_id, ms.id, nc.secret_ciphertext, nr.interval_seconds
		 FROM notification_rules nr
		 JOIN notification_channels nc ON nc.user_id = nr.user_id
		 JOIN monitor_sessions ms ON ms.user_id = nr.user_id
		   AND ms.status = 1
		   AND ms.client_id IS NOT NULL
		   AND ms.mode = 'monitor'
		   AND ms.running = 1
		 WHERE nr.rule_key = ? AND nr.enabled = 1
		   AND nc.provider = ? AND nc.enabled = 1
		 ORDER BY nr.user_id, ms.last_publish_at DESC, ms.created_at DESC`,
		RuleEXPStalled,
		ProviderBark,
	)
	if err != nil {
		s.logger.Error("query EXP stalled rules failed", "error", err)
		return
	}
	defer rows.Close()

	var rules []stalledEXPRule
	for rows.Next() {
		var rule stalledEXPRule
		if err := rows.Scan(&rule.UserID, &rule.SessionID, &rule.SecretCiphertext, &rule.ThresholdSeconds); err != nil {
			s.logger.Error("scan EXP stalled rule failed", "error", err)
			continue
		}
		rules = append(rules, rule)
	}

	handled := make(map[int64]bool)
	for _, rule := range rules {
		if handled[rule.UserID] {
			continue
		}
		exp, online, ok := s.hub.LatestEXP(rule.SessionID)
		if !ok || !online || exp.CurrentEXP == nil || exp.Percent == nil {
			continue
		}
		handled[rule.UserID] = true
		s.evaluateEXPStall(ctx, rule, time.Now())
	}
	for _, rule := range rules {
		if handled[rule.UserID] {
			continue
		}
		handled[rule.UserID] = true
		_, _ = s.db.ExecContext(
			ctx,
			`DELETE FROM notification_rule_states WHERE user_id = ? AND rule_key = ?`,
			rule.UserID,
			RuleEXPStalled,
		)
	}
}

func (s *Service) evaluateEXPStall(ctx context.Context, rule stalledEXPRule, now time.Time) {
	exp, online, ok := s.hub.LatestEXP(rule.SessionID)
	if !ok || !online || exp.CurrentEXP == nil || exp.Percent == nil {
		_, _ = s.db.ExecContext(
			ctx,
			`DELETE FROM notification_rule_states WHERE user_id = ? AND rule_key = ?`,
			rule.UserID,
			RuleEXPStalled,
		)
		return
	}
	count, due, err := s.reserveEXPStallNotification(
		ctx,
		rule.UserID,
		rule.SessionID,
		*exp.CurrentEXP,
		time.Duration(normalizeStallSeconds(rule.ThresholdSeconds))*time.Second,
		now,
	)
	if err != nil {
		s.logger.Error("track EXP stalled notification failed", "user_id", rule.UserID, "error", err)
		return
	}
	if !due {
		return
	}

	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE notification_rules SET last_sent_at = NOW(3)
		 WHERE user_id = ? AND rule_key = ? AND enabled = 1`,
		rule.UserID,
		RuleEXPStalled,
	); err != nil {
		s.logger.Error("update EXP stalled notification time failed", "user_id", rule.UserID, "error", err)
	}
	deviceKey, err := s.box.open(rule.SecretCiphertext)
	if err == nil {
		stalledSeconds := int64(normalizeStallSeconds(rule.ThresholdSeconds)) * int64(count)
		body := fmt.Sprintf(
			"%d秒经验无增长，疑似卡技能\nEXP %s · %s%%",
			stalledSeconds,
			formatInteger(*exp.CurrentEXP),
			formatPercent(*exp.Percent),
		)
		err = s.bark.push(ctx, deviceKey, body, "")
	}
	s.recordDelivery(ctx, rule.UserID, RuleEXPStalled, err)
	if err != nil {
		s.logger.Warn("send Bark EXP stalled notification failed", "user_id", rule.UserID, "error", err)
	}
}

func (s *Service) reserveEXPStallNotification(
	ctx context.Context,
	userID int64,
	sessionID string,
	currentEXP int64,
	threshold time.Duration,
	now time.Time,
) (int, bool, error) {
	var storedSessionID string
	var lastEXP int64
	var lastChanged time.Time
	var lastNotification sql.NullTime
	var count int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT session_id, last_numeric_value, last_changed_at, last_notification_at, consecutive_count
		 FROM notification_rule_states WHERE user_id = ? AND rule_key = ? LIMIT 1`,
		userID,
		RuleEXPStalled,
	).Scan(&storedSessionID, &lastEXP, &lastChanged, &lastNotification, &count)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(
			ctx,
			`INSERT INTO notification_rule_states
			 (user_id, rule_key, session_id, last_numeric_value, last_changed_at, consecutive_count)
			 VALUES (?, ?, ?, ?, ?, 0)`,
			userID,
			RuleEXPStalled,
			sessionID,
			currentEXP,
			now,
		)
		return 0, false, err
	}
	if err != nil {
		return 0, false, err
	}
	if storedSessionID != sessionID || lastEXP != currentEXP {
		_, err = s.db.ExecContext(
			ctx,
			`UPDATE notification_rule_states
			 SET session_id = ?, last_numeric_value = ?, last_changed_at = ?,
			     last_notification_at = NULL, consecutive_count = 0
			 WHERE user_id = ? AND rule_key = ?`,
			sessionID,
			currentEXP,
			now,
			userID,
			RuleEXPStalled,
		)
		return 0, false, err
	}
	if !stallNotificationDue(lastChanged, lastNotification, threshold, now) {
		return count, false, nil
	}
	count++
	_, err = s.db.ExecContext(
		ctx,
		`UPDATE notification_rule_states
		 SET last_notification_at = ?, consecutive_count = ?
		 WHERE user_id = ? AND rule_key = ?`,
		now,
		count,
		userID,
		RuleEXPStalled,
	)
	return count, err == nil, err
}

func stallNotificationDue(lastChanged time.Time, lastNotification sql.NullTime, threshold time.Duration, now time.Time) bool {
	if now.Sub(lastChanged) < threshold {
		return false
	}
	return !lastNotification.Valid || now.Sub(lastNotification.Time) >= threshold
}

func normalizeStallSeconds(value int) int {
	if value < MinStallSeconds || value > MaxStallSeconds {
		return DefaultStallSeconds
	}
	return value
}

type dueRuneAlertRule struct {
	UserID           int64
	SessionID        string
	SecretCiphertext string
	LastSentAt       sql.NullTime
	IntervalSeconds  int
	UrgentMuted      bool
}

// processDueRuneAlerts scans every enabled rule so an observed cleared state can
// close the current event cycle. The next detected state can then make sound once.
func (s *Service) processDueRuneAlerts(ctx context.Context) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT nr.user_id, COALESCE(ms.id, ''), nc.secret_ciphertext, nr.last_sent_at,
		        nr.interval_seconds, nc.urgent_alerts_muted
		 FROM notification_rules nr
		 JOIN notification_channels nc ON nc.user_id = nr.user_id
		 LEFT JOIN monitor_sessions ms ON ms.user_id = nr.user_id
		   AND ms.status = 1
		   AND ms.client_id IS NOT NULL
		   AND ms.mode = 'monitor'
		   AND ms.running = 1
		 WHERE nr.rule_key = ? AND nr.enabled = 1
		   AND nc.provider = ? AND nc.enabled = 1
		 ORDER BY nr.user_id, ms.last_publish_at DESC, ms.created_at DESC`,
		RuleRuneAlert,
		ProviderBark,
	)
	if err != nil {
		s.logger.Error("query due rune alerts failed", "error", err)
		return
	}
	defer rows.Close()

	var due []dueRuneAlertRule
	for rows.Next() {
		var item dueRuneAlertRule
		if err := rows.Scan(
			&item.UserID,
			&item.SessionID,
			&item.SecretCiphertext,
			&item.LastSentAt,
			&item.IntervalSeconds,
			&item.UrgentMuted,
		); err != nil {
			s.logger.Error("scan due rune alert failed", "error", err)
			continue
		}
		due = append(due, item)
	}
	seenUsers := make(map[int64]bool)
	activeUsers := make(map[int64]bool)
	for _, item := range due {
		seenUsers[item.UserID] = true
		if activeUsers[item.UserID] {
			continue
		}
		now := time.Now()
		payload, online, ok := s.hub.LatestRune(item.SessionID)
		if !ok || !online || !runeAlertActive(payload, now) {
			continue
		}
		activeUsers[item.UserID] = true
		if alertNotificationDue(item.LastSentAt, item.IntervalSeconds, now, 0) {
			s.sendRuneAlert(ctx, item, now)
		}
	}
	for userID := range seenUsers {
		if !activeUsers[userID] {
			s.resetAlertCycle(ctx, userID, RuleRuneAlert)
		}
	}
}

func (s *Service) sendRuneAlert(ctx context.Context, item dueRuneAlertRule, now time.Time) {
	payload, online, ok := s.hub.LatestRune(item.SessionID)
	if !ok || !online || !runeAlertActive(payload, now) {
		return
	}
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE notification_rules SET last_sent_at = NOW(3)
		 WHERE user_id = ? AND rule_key = ? AND enabled = 1`,
		item.UserID,
		RuleRuneAlert,
	); err != nil {
		s.logger.Error("reserve rune alert schedule failed", "user_id", item.UserID, "error", err)
		return
	}
	deviceKey, err := s.box.open(item.SecretCiphertext)
	if err == nil {
		err = s.bark.pushCriticalWithURL(
			ctx,
			deviceKey,
			"出现紫色符文，尽快解除！",
			s.publicBaseURL,
			urgentAlertVolume(!item.LastSentAt.Valid, item.UrgentMuted),
		)
	}
	s.recordDelivery(ctx, item.UserID, RuleRuneAlert, err)
	if err != nil {
		s.logger.Warn("send Bark rune alert failed", "user_id", item.UserID, "error", err)
	}
}

// runeAlertActive 判断上报的符文状态是否既处于「出现」又足够新鲜。
// 客户端掉线或停止监控后上报会停止，过期数据不再触发推送。
func runeAlertActive(payload protocol.RunePayload, now time.Time) bool {
	if !payload.Detected {
		return false
	}
	if payload.DetectedAt <= 0 {
		return false
	}
	age := now.Sub(time.UnixMilli(payload.DetectedAt))
	return age >= -RuneAlertFreshnessWindow && age <= RuneAlertFreshnessWindow
}

type dueMouseFollowVerificationRule struct {
	UserID           int64
	SessionID        string
	SecretCiphertext string
	LastSentAt       sql.NullTime
	IntervalSeconds  int
	UrgentMuted      bool
}

// processDueMouseFollowVerifications 每秒扫描一次，让首次紧急推送尽量在 1 秒内发出。
func (s *Service) processDueMouseFollowVerifications(ctx context.Context) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT nr.user_id, COALESCE(ms.id, ''), nc.secret_ciphertext, nr.last_sent_at,
		        nr.interval_seconds, nc.urgent_alerts_muted
		 FROM notification_rules nr
		 JOIN notification_channels nc ON nc.user_id = nr.user_id
		 LEFT JOIN monitor_sessions ms ON ms.user_id = nr.user_id
		   AND ms.status = 1
		   AND ms.client_id IS NOT NULL
		   AND ms.mode = 'monitor'
		   AND ms.running = 1
		 WHERE nr.rule_key = ? AND nr.enabled = 1
		   AND nc.provider = ? AND nc.enabled = 1
		 ORDER BY nr.user_id, ms.last_publish_at DESC, ms.created_at DESC`,
		RuleMouseFollowVerification,
		ProviderBark,
	)
	if err != nil {
		s.logger.Error("query due mouse follow verification alerts failed", "error", err)
		return
	}
	defer rows.Close()

	var due []dueMouseFollowVerificationRule
	for rows.Next() {
		var item dueMouseFollowVerificationRule
		if err := rows.Scan(
			&item.UserID,
			&item.SessionID,
			&item.SecretCiphertext,
			&item.LastSentAt,
			&item.IntervalSeconds,
			&item.UrgentMuted,
		); err != nil {
			s.logger.Error("scan due mouse follow verification alert failed", "error", err)
			continue
		}
		due = append(due, item)
	}

	seenUsers := make(map[int64]bool)
	activeUsers := make(map[int64]bool)
	for _, item := range due {
		seenUsers[item.UserID] = true
		if activeUsers[item.UserID] {
			continue
		}
		now := time.Now()
		payload, online, ok := s.hub.LatestVerification(item.SessionID)
		if !ok || !online || !mouseFollowVerificationActive(payload, now) {
			continue
		}
		activeUsers[item.UserID] = true
		if alertNotificationDue(item.LastSentAt, item.IntervalSeconds, now, 0) {
			s.sendMouseFollowVerification(ctx, item, now)
		}
	}
	for userID := range seenUsers {
		if !activeUsers[userID] {
			s.resetAlertCycle(ctx, userID, RuleMouseFollowVerification)
		}
	}
}

func (s *Service) sendMouseFollowVerification(
	ctx context.Context,
	item dueMouseFollowVerificationRule,
	now time.Time,
) {
	payload, online, ok := s.hub.LatestVerification(item.SessionID)
	if !ok || !online || !mouseFollowVerificationActive(payload, now) {
		return
	}
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE notification_rules SET last_sent_at = NOW(3)
		 WHERE user_id = ? AND rule_key = ? AND enabled = 1`,
		item.UserID,
		RuleMouseFollowVerification,
	); err != nil {
		s.logger.Error(
			"reserve mouse follow verification schedule failed",
			"user_id",
			item.UserID,
			"error",
			err,
		)
		return
	}
	deviceKey, err := s.box.open(item.SecretCiphertext)
	if err == nil {
		err = s.bark.pushCriticalWithURL(
			ctx,
			deviceKey,
			"出现鼠标跟随验证，请立即处理！",
			s.publicBaseURL,
			urgentAlertVolume(!item.LastSentAt.Valid, item.UrgentMuted),
		)
	}
	s.recordDelivery(ctx, item.UserID, RuleMouseFollowVerification, err)
	if err != nil {
		s.logger.Warn(
			"send Bark mouse follow verification failed",
			"user_id",
			item.UserID,
			"error",
			err,
		)
	}
}

func mouseFollowVerificationActive(
	payload protocol.VerificationPayload,
	now time.Time,
) bool {
	if !payload.Detected || payload.DetectedAt <= 0 {
		return false
	}
	age := now.Sub(time.UnixMilli(payload.DetectedAt))
	return age >= -MouseFollowVerificationFreshnessWindow &&
		age <= MouseFollowVerificationFreshnessWindow
}

func alertNotificationDue(
	lastSentAt sql.NullTime,
	intervalSeconds int,
	now time.Time,
	slack time.Duration,
) bool {
	if !lastSentAt.Valid {
		return true
	}
	return now.Add(slack).Sub(lastSentAt.Time) >= time.Duration(intervalSeconds)*time.Second
}

func urgentAlertVolume(firstInCycle, muted bool) int {
	if firstInCycle && !muted {
		return 4
	}
	return 0
}

func (s *Service) resetAlertCycle(ctx context.Context, userID int64, ruleKey string) {
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE notification_rules SET last_sent_at = NULL
		 WHERE user_id = ? AND rule_key = ? AND last_sent_at IS NOT NULL`,
		userID,
		ruleKey,
	); err != nil {
		s.logger.Error(
			"reset alert notification cycle failed",
			"user_id",
			userID,
			"rule_key",
			ruleKey,
			"error",
			err,
		)
	}
}

type dueZoneBreachRule struct {
	UserID           int64
	SessionID        string
	SecretCiphertext string
}

// processDueZoneBreaches 找出所有开启了越界报警、且距上次推送已满 5 秒的用户。
func (s *Service) processDueZoneBreaches(ctx context.Context) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT nr.user_id, ms.id, nc.secret_ciphertext
		 FROM notification_rules nr
		 JOIN notification_channels nc ON nc.user_id = nr.user_id
		 JOIN monitor_sessions ms ON ms.user_id = nr.user_id
		   AND ms.status = 1
		   AND ms.client_id IS NOT NULL
		   AND ms.mode = 'monitor'
		   AND ms.running = 1
		 WHERE nr.rule_key = ? AND nr.enabled = 1
		   AND nc.provider = ? AND nc.enabled = 1
		   AND (nr.last_sent_at IS NULL
		        OR TIMESTAMPADD(SECOND, nr.interval_seconds, nr.last_sent_at)
		           <= TIMESTAMPADD(MICROSECOND, ?, NOW(3)))
		 ORDER BY nr.user_id, ms.last_publish_at DESC, ms.created_at DESC`,
		RuleZoneBreach,
		ProviderBark,
		scheduleSlack.Microseconds(),
	)
	if err != nil {
		s.logger.Error("query due zone breaches failed", "error", err)
		return
	}
	defer rows.Close()

	var due []dueZoneBreachRule
	for rows.Next() {
		var item dueZoneBreachRule
		if err := rows.Scan(&item.UserID, &item.SessionID, &item.SecretCiphertext); err != nil {
			s.logger.Error("scan due zone breach failed", "error", err)
			continue
		}
		due = append(due, item)
	}
	sentUsers := make(map[int64]bool)
	for _, item := range due {
		if sentUsers[item.UserID] {
			continue
		}
		payload, online, ok := s.hub.LatestZone(item.SessionID)
		if !ok || !online || !zoneBreachActive(payload, time.Now()) {
			continue
		}
		sentUsers[item.UserID] = true
		s.sendZoneBreach(ctx, item, time.Now())
	}
}

func (s *Service) sendZoneBreach(ctx context.Context, item dueZoneBreachRule, now time.Time) {
	payload, online, ok := s.hub.LatestZone(item.SessionID)
	if !ok || !online || !zoneBreachActive(payload, now) {
		return
	}
	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE notification_rules SET last_sent_at = NOW(3)
		 WHERE user_id = ? AND rule_key = ? AND enabled = 1`,
		item.UserID,
		RuleZoneBreach,
	); err != nil {
		s.logger.Error("reserve zone breach schedule failed", "user_id", item.UserID, "error", err)
		return
	}
	deviceKey, err := s.box.open(item.SecretCiphertext)
	if err == nil {
		err = s.bark.push(ctx, deviceKey, "角色已离开安全区，请尽快查看！", s.publicBaseURL)
	}
	s.recordDelivery(ctx, item.UserID, RuleZoneBreach, err)
	if err != nil {
		s.logger.Warn("send Bark zone breach failed", "user_id", item.UserID, "error", err)
	}
}

// zoneBreachActive 判断上报的安全区状态是否既处于「已越界」又足够新鲜。
func zoneBreachActive(payload protocol.ZonePayload, now time.Time) bool {
	if !payload.Outside {
		return false
	}
	if payload.DetectedAt <= 0 {
		return false
	}
	age := now.Sub(time.UnixMilli(payload.DetectedAt))
	return age >= -ZoneBreachFreshnessWindow && age <= ZoneBreachFreshnessWindow
}

func (s *Service) recordDelivery(ctx context.Context, userID int64, eventType string, sendError error) {
	status := "sent"
	errorMessage := ""
	if sendError != nil {
		status = "failed"
		errorMessage = sendError.Error()
		if len(errorMessage) > 500 {
			errorMessage = errorMessage[:500]
		}
	}
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO notification_deliveries (user_id, provider, event_type, status, error_message)
		 VALUES (?, ?, ?, ?, NULLIF(?, ''))`,
		userID,
		ProviderBark,
		eventType,
		status,
		errorMessage,
	)
	if err != nil {
		var mysqlError *mysql.MySQLError
		if !errors.As(err, &mysqlError) || mysqlError.Number != 1146 {
			s.logger.Error("record notification delivery failed", "error", err)
		}
	}
}

func formatInteger(value int64) string {
	raw := fmt.Sprintf("%d", value)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "," + raw[i:]
	}
	return raw
}

func formatPercent(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", value), "0"), ".")
}
