package expgain

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"autobuff-monitor/server/internal/protocol"
	"autobuff-monitor/server/internal/realtime"
)

const (
	sampleInterval   = 5 * time.Second
	flushInterval    = 15 * time.Second
	window10m        = 10 * time.Minute
	window1h         = time.Hour
	sampleRetain     = time.Hour
	shanghaiLocation = "Asia/Shanghai"
)

type sample struct {
	At     time.Time
	Gained int64
}

type userState struct {
	totalGained    int64
	dailyGained    int64
	dailyDate      time.Time // 日历日零点（上海时区）
	lastCurrentEXP *int64
	sessionID      string
	samples        []sample
	dirty          bool
	lastPayload    protocol.GainPayload
	hasPayload     bool
}

// Service 根据活跃会话的 EXP 读数累计经验获取量，定时落库，并推送给查看端。
type Service struct {
	db     *sql.DB
	hub    *realtime.Hub
	logger *slog.Logger
	loc    *time.Location

	mu     sync.Mutex
	states map[int64]*userState
}

func NewService(db *sql.DB, hub *realtime.Hub, logger *slog.Logger) (*Service, error) {
	loc, err := time.LoadLocation(shanghaiLocation)
	if err != nil {
		return nil, err
	}
	service := &Service{
		db:     db,
		hub:    hub,
		logger: logger,
		loc:    loc,
		states: make(map[int64]*userState),
	}
	if err := service.loadFromDB(context.Background()); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		lastFlush := time.Now()
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				s.tick(ctx, now)
				if now.Sub(lastFlush) >= flushInterval {
					s.flushDirty(ctx)
					lastFlush = now
				}
			case <-ctx.Done():
				// 停服前尽量把内存里的累计写回库，避免丢掉最近十几秒的增量。
				s.flushDirty(context.Background())
				return
			}
		}
	}()
}

// Snapshot 返回指定用户当前的累计快照（供 HTTP 查询或清零后回传）。
func (s *Service) Snapshot(userID int64, now time.Time) protocol.GainPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.ensureStateLocked(userID, now)
	s.rollDailyLocked(state, now)
	s.pruneSamplesLocked(state, now)
	return s.payloadLocked(state, now)
}

// ResetTotal 只清零跨启动总累计，当日与流量窗口保留。
func (s *Service) ResetTotal(ctx context.Context, userID int64) (protocol.GainPayload, error) {
	now := time.Now()
	s.mu.Lock()
	state := s.ensureStateLocked(userID, now)
	s.rollDailyLocked(state, now)
	state.totalGained = 0
	state.dirty = true
	payload := s.payloadLocked(state, now)
	state.lastPayload = payload
	state.hasPayload = true
	sessionID := state.sessionID
	s.mu.Unlock()

	if err := s.persistUser(ctx, userID); err != nil {
		return protocol.GainPayload{}, err
	}
	if sessionID != "" {
		if err := s.hub.PublishGain(sessionID, payload); err != nil {
			s.logger.Warn("broadcast gain after reset failed", "user_id", userID, "error", err)
		}
	}
	return payload, nil
}

func (s *Service) tick(ctx context.Context, now time.Time) {
	sessions, err := s.activeSessions(ctx)
	if err != nil {
		s.logger.Error("query active sessions for exp gain failed", "error", err)
		return
	}

	type touched struct {
		userID    int64
		sessionID string
		payload   protocol.GainPayload
	}
	var broadcasts []touched

	s.mu.Lock()
	for _, session := range sessions {
		state := s.ensureStateLocked(session.UserID, now)
		s.rollDailyLocked(state, now)
		s.sampleSessionLocked(state, session.SessionID, now)
		s.pruneSamplesLocked(state, now)
		payload := s.payloadLocked(state, now)
		// SampledAt 每次都会变，比较时忽略它，避免无变化时每 5 秒刷屏。
		if !state.hasPayload || !sameGainValues(payload, state.lastPayload) {
			state.lastPayload = payload
			state.hasPayload = true
			broadcasts = append(broadcasts, touched{
				userID:    session.UserID,
				sessionID: session.SessionID,
				payload:   payload,
			})
		}
	}

	// 没有活跃会话时也要日切标记脏数据，等下次 flush 落库。
	today := calendarDate(now, s.loc)
	for _, state := range s.states {
		if state.dailyDate.Before(today) {
			s.rollDailyLocked(state, now)
		}
	}
	s.mu.Unlock()

	for _, item := range broadcasts {
		if err := s.hub.PublishGain(item.sessionID, item.payload); err != nil {
			s.logger.Warn("broadcast gain failed", "user_id", item.userID, "error", err)
		}
	}
}

type activeSession struct {
	UserID    int64
	SessionID string
}

func (s *Service) activeSessions(ctx context.Context) ([]activeSession, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT user_id, id FROM monitor_sessions WHERE status = 1`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []activeSession
	for rows.Next() {
		var item activeSession
		if err := rows.Scan(&item.UserID, &item.SessionID); err != nil {
			return nil, err
		}
		sessions = append(sessions, item)
	}
	return sessions, rows.Err()
}

func (s *Service) sampleSessionLocked(state *userState, sessionID string, now time.Time) {
	if state.sessionID != sessionID {
		// 会话重建后重新建基准，避免把「新旧会话读数差」算成一次巨大获取。
		state.sessionID = sessionID
		state.lastCurrentEXP = nil
		state.dirty = true
	}

	exp, online, ok := s.hub.LatestEXP(sessionID)
	if !ok || !online || exp.CurrentEXP == nil {
		// 读不到经验时清掉基准，与前端写入速率、服务端停滞逻辑一致。
		state.lastCurrentEXP = nil
		return
	}

	current := *exp.CurrentEXP
	previous := state.lastCurrentEXP
	state.lastCurrentEXP = &current
	if previous == nil {
		state.dirty = true
		return
	}

	delta := current - *previous
	if delta <= 0 {
		// 升级或误识别导致回落时，负增量归零，不减少累计。
		return
	}

	state.totalGained += delta
	state.dailyGained += delta
	state.samples = append(state.samples, sample{At: now, Gained: delta})
	state.dirty = true
}

func (s *Service) ensureStateLocked(userID int64, now time.Time) *userState {
	state := s.states[userID]
	if state != nil {
		return state
	}
	state = &userState{
		dailyDate: calendarDate(now, s.loc),
	}
	s.states[userID] = state
	return state
}

func (s *Service) rollDailyLocked(state *userState, now time.Time) {
	today := calendarDate(now, s.loc)
	if !state.dailyDate.Before(today) {
		return
	}
	state.dailyDate = today
	state.dailyGained = 0
	state.dirty = true
}

func (s *Service) pruneSamplesLocked(state *userState, now time.Time) {
	cutoff := now.Add(-sampleRetain)
	kept := state.samples[:0]
	for _, item := range state.samples {
		if item.At.After(cutoff) {
			kept = append(kept, item)
		}
	}
	state.samples = kept
}

func (s *Service) payloadLocked(state *userState, now time.Time) protocol.GainPayload {
	return protocol.GainPayload{
		Inflow10m:  sumWindow(state.samples, now, window10m),
		Outflow1h:  sumWindow(state.samples, now, window1h),
		TotalUsage: state.totalGained,
		DailyUsage: state.dailyGained,
		SampledAt:  now.UnixMilli(),
	}
}

func sumWindow(samples []sample, now time.Time, window time.Duration) int64 {
	cutoff := now.Add(-window)
	var total int64
	for _, item := range samples {
		if !item.At.Before(cutoff) {
			total += item.Gained
		}
	}
	return total
}

func sameGainValues(a, b protocol.GainPayload) bool {
	return a.Inflow10m == b.Inflow10m &&
		a.Outflow1h == b.Outflow1h &&
		a.TotalUsage == b.TotalUsage &&
		a.DailyUsage == b.DailyUsage
}

func calendarDate(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

func (s *Service) loadFromDB(ctx context.Context) error {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT user_id, total_gained, daily_gained, daily_date, last_current_exp, session_id
		 FROM exp_gain_stats`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var (
			userID         int64
			totalGained    int64
			dailyGained    int64
			dailyDate      time.Time
			lastCurrentEXP sql.NullInt64
			sessionID      sql.NullString
		)
		if err := rows.Scan(&userID, &totalGained, &dailyGained, &dailyDate, &lastCurrentEXP, &sessionID); err != nil {
			return err
		}
		state := &userState{
			totalGained: totalGained,
			dailyGained: dailyGained,
			dailyDate:   calendarDate(dailyDate, s.loc),
		}
		if lastCurrentEXP.Valid {
			value := lastCurrentEXP.Int64
			state.lastCurrentEXP = &value
		}
		if sessionID.Valid {
			state.sessionID = sessionID.String
		}
		s.rollDailyLocked(state, now)
		s.states[userID] = state
	}
	if err := rows.Err(); err != nil {
		return err
	}

	sampleRows, err := s.db.QueryContext(
		ctx,
		`SELECT user_id, gained, sampled_at FROM exp_gain_samples
		 WHERE sampled_at >= ? ORDER BY sampled_at ASC`,
		now.Add(-sampleRetain),
	)
	if err != nil {
		return err
	}
	defer sampleRows.Close()

	for sampleRows.Next() {
		var (
			userID    int64
			gained    int64
			sampledAt time.Time
		)
		if err := sampleRows.Scan(&userID, &gained, &sampledAt); err != nil {
			return err
		}
		state := s.ensureStateLocked(userID, now)
		state.samples = append(state.samples, sample{At: sampledAt, Gained: gained})
	}
	return sampleRows.Err()
}

func (s *Service) flushDirty(ctx context.Context) {
	s.mu.Lock()
	var dirtyIDs []int64
	for userID, state := range s.states {
		if state.dirty {
			dirtyIDs = append(dirtyIDs, userID)
		}
	}
	s.mu.Unlock()

	for _, userID := range dirtyIDs {
		if err := s.persistUser(ctx, userID); err != nil {
			s.logger.Error("persist exp gain failed", "user_id", userID, "error", err)
		}
	}

	if _, err := s.db.ExecContext(
		ctx,
		`DELETE FROM exp_gain_samples WHERE sampled_at < ?`,
		time.Now().Add(-sampleRetain),
	); err != nil {
		s.logger.Error("prune exp gain samples failed", "error", err)
	}
}

func (s *Service) persistUser(ctx context.Context, userID int64) error {
	s.mu.Lock()
	state := s.states[userID]
	if state == nil {
		s.mu.Unlock()
		return errors.New("unknown user state")
	}
	total := state.totalGained
	daily := state.dailyGained
	dailyDate := state.dailyDate
	sessionID := state.sessionID
	var lastEXP any
	if state.lastCurrentEXP != nil {
		lastEXP = *state.lastCurrentEXP
	}
	samples := append([]sample(nil), state.samples...)
	s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var sessionValue any
	if sessionID != "" {
		sessionValue = sessionID
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO exp_gain_stats
		   (user_id, total_gained, daily_gained, daily_date, last_current_exp, session_id, last_sampled_at)
		 VALUES (?, ?, ?, ?, ?, ?, NOW(3))
		 ON DUPLICATE KEY UPDATE
		   total_gained = VALUES(total_gained),
		   daily_gained = VALUES(daily_gained),
		   daily_date = VALUES(daily_date),
		   last_current_exp = VALUES(last_current_exp),
		   session_id = VALUES(session_id),
		   last_sampled_at = VALUES(last_sampled_at)`,
		userID,
		total,
		daily,
		dailyDate.In(s.loc).Format("2006-01-02"),
		lastEXP,
		sessionValue,
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM exp_gain_samples WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, item := range samples {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO exp_gain_samples (user_id, gained, sampled_at) VALUES (?, ?, ?)`,
			userID,
			item.Gained,
			item.At,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	s.mu.Lock()
	if current := s.states[userID]; current != nil {
		current.dirty = false
	}
	s.mu.Unlock()
	return nil
}
