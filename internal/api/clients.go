package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"autobuff-monitor/server/internal/protocol"

	"github.com/coder/websocket"
)

type clientItem struct {
	ID         string `json:"id"`
	ClientID   string `json:"clientId"`
	Name       string `json:"name"`
	RoleName   string `json:"roleName"`
	Mode       string `json:"mode"`
	Running    bool   `json:"running"`
	Online     bool   `json:"online"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeenAt *int64 `json:"lastSeenAt"`
}

func (s *Server) clientItems(ctx context.Context, userID int64) ([]clientItem, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, client_id, name, role_name, mode, running, created_at, last_publish_at
		 FROM monitor_sessions
		 WHERE user_id = ? AND status = 1 AND client_id IS NOT NULL
		 ORDER BY last_publish_at DESC, created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]clientItem, 0)
	for rows.Next() {
		var item clientItem
		var running uint8
		var createdAt time.Time
		var lastSeen sql.NullTime
		if err := rows.Scan(&item.ID, &item.ClientID, &item.Name, &item.RoleName, &item.Mode, &running, &createdAt, &lastSeen); err != nil {
			return nil, err
		}
		item.Running = running == 1
		item.CreatedAt = createdAt.UnixMilli()
		online, state, _ := s.hub.ClientStatus(item.ID)
		item.Online = online
		if item.Online && state.Mode != "" {
			item.Mode = state.Mode
			item.Running = state.Running
		}
		if lastSeen.Valid {
			value := lastSeen.Time.UnixMilli()
			item.LastSeenAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type ropeTeamMemberItem struct {
	SessionID         string `json:"sessionId"`
	ClientID          string `json:"clientId"`
	Name              string `json:"name"`
	RoleName          string `json:"roleName"`
	IsLeader          bool   `json:"isLeader"`
	Joined            bool   `json:"joined"`
	Invited           bool   `json:"invited"`
	BossBuffCompleted bool   `json:"bossBuffCompleted"`
	Online            bool   `json:"online"`
}

type ropeTeamItem struct {
	ID              int64                `json:"id"`
	LeaderSessionID string               `json:"leaderSessionId"`
	CreatedInGame   bool                 `json:"createdInGame"`
	BossRoleName    string               `json:"bossRoleName"`
	BossCycleID     int64                `json:"bossCycleId"`
	BossCycleState  string               `json:"bossCycleState"`
	Members         []ropeTeamMemberItem `json:"members"`
}

func (s *Server) ropeTeam(ctx context.Context, userID int64) (*ropeTeamItem, error) {
	var team ropeTeamItem
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, leader_session_id, created_in_game, boss_role_name, boss_cycle_id, boss_cycle_state
		 FROM rope_teams WHERE user_id = ? LIMIT 1`,
		userID,
	).Scan(
		&team.ID,
		&team.LeaderSessionID,
		&team.CreatedInGame,
		&team.BossRoleName,
		&team.BossCycleID,
		&team.BossCycleState,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT ms.id, ms.client_id, ms.name, ms.role_name, rtm.joined, rtm.invited,
		        rtm.boss_completed_cycle_id
		 FROM rope_team_members rtm
		 JOIN monitor_sessions ms ON ms.id = rtm.session_id
		 WHERE rtm.team_id = ? AND ms.status = 1
		 ORDER BY (ms.id = ?) DESC, ms.created_at ASC`,
		team.ID, team.LeaderSessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	team.Members = make([]ropeTeamMemberItem, 0)
	for rows.Next() {
		var member ropeTeamMemberItem
		var joined uint8
		var invited uint8
		var completedCycleID int64
		if err := rows.Scan(
			&member.SessionID,
			&member.ClientID,
			&member.Name,
			&member.RoleName,
			&joined,
			&invited,
			&completedCycleID,
		); err != nil {
			return nil, err
		}
		member.IsLeader = member.SessionID == team.LeaderSessionID
		member.Joined = joined == 1
		member.Invited = invited == 1
		member.BossBuffCompleted = team.BossCycleID > 0 && completedCycleID == team.BossCycleID
		member.Online, _, _ = s.hub.ClientStatus(member.SessionID)
		team.Members = append(team.Members, member)
	}
	return &team, rows.Err()
}

func (s *Server) handleRopeTeam(w http.ResponseWriter, r *http.Request) {
	team, err := s.ropeTeam(r.Context(), mustUser(r.Context()).ID)
	if err != nil {
		s.internalError(w, "load rope team failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team})
}

func (s *Server) handleSaveRopeTeamBoss(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RoleName string `json:"roleName"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.RoleName = strings.TrimSpace(request.RoleName)
	if !validRoleName(request.RoleName) {
		writeError(w, http.StatusBadRequest, "invalid_boss_role_name", "目标老板名称须为 1–24 个字符且不能包含控制字符")
		return
	}
	userID := mustUser(r.Context()).ID
	var cycleState string
	var leaderSessionID string
	var currentBossRoleName string
	var createdInGame uint8
	err := s.db.QueryRowContext(
		r.Context(),
		`SELECT boss_cycle_state, created_in_game, leader_session_id, boss_role_name
		 FROM rope_teams WHERE user_id = ? LIMIT 1`,
		userID,
	).Scan(&cycleState, &createdInGame, &leaderSessionID, &currentBossRoleName)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "rope_team_not_found", "请先创建挂绳队伍")
		return
	}
	if err != nil {
		s.internalError(w, "load rope team boss state failed", err)
		return
	}
	if cycleState != "idle" {
		if currentBossRoleName == request.RoleName {
			team, loadErr := s.ropeTeam(r.Context(), userID)
			if loadErr != nil {
				s.internalError(w, "reload active rope team boss failed", loadErr)
				return
			}
			leaderOnline, _, _ := s.hub.ClientStatus(leaderSessionID)
			writeJSON(w, http.StatusOK, map[string]any{
				"team":          team,
				"cycleStarted":  false,
				"leaderOnline":  leaderOnline,
				"alreadyActive": true,
				"startReason":   "already_active",
			})
			return
		}
		writeError(w, http.StatusConflict, "boss_cycle_active", "老板 Buff 流程运行中，暂时不能修改目标老板")
		return
	}
	if createdInGame == 0 {
		writeError(w, http.StatusConflict, "rope_team_not_created", "请等待队长客户端完成游戏内建队")
		return
	}
	leaderOnline, _, _ := s.hub.ClientStatus(leaderSessionID)
	cycleStarted := false
	startReason := "leader_offline"
	if leaderOnline {
		cycleStarted, startReason = s.startBossBuffCycle(
			r.Context(), userID, leaderSessionID, request.RoleName,
		)
	}
	if !cycleStarted {
		if _, err = s.db.ExecContext(
			r.Context(),
			`UPDATE rope_teams SET boss_role_name = ?
			 WHERE user_id = ? AND boss_cycle_state = 'idle'`,
			request.RoleName, userID,
		); err != nil {
			s.internalError(w, "save rope team boss failed", err)
			return
		}
	}
	s.hub.NotifyClients(userID)
	team, loadErr := s.ropeTeam(r.Context(), userID)
	if loadErr != nil {
		s.internalError(w, "reload rope team after boss save failed", loadErr)
		return
	}
	if !cycleStarted && team != nil && team.BossCycleState != "idle" && team.BossRoleName != request.RoleName {
		writeError(w, http.StatusConflict, "boss_cycle_active", "老板 Buff 流程已由另一项保存操作启动，请等待本轮结束")
		return
	}
	alreadyActive := !cycleStarted && team != nil &&
		team.BossCycleState != "idle" && team.BossRoleName == request.RoleName
	writeJSON(w, http.StatusOK, map[string]any{
		"team":          team,
		"cycleStarted":  cycleStarted,
		"leaderOnline":  leaderOnline,
		"alreadyActive": alreadyActive,
		"startReason":   startReason,
	})
}

func validRoleName(value string) bool {
	runes := []rune(value)
	if len(runes) == 0 || len(runes) > 24 {
		return false
	}
	for _, value := range runes {
		if value < 0x20 || value == 0x7f {
			return false
		}
	}
	return true
}

func (s *Server) handleSaveClientRoleName(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ClientID string `json:"clientId"`
		RoleName string `json:"roleName"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.ClientID = strings.TrimSpace(request.ClientID)
	request.RoleName = strings.TrimSpace(request.RoleName)
	if len(request.ClientID) < 8 || len(request.ClientID) > 64 || !validRoleName(request.RoleName) {
		writeError(w, http.StatusBadRequest, "invalid_role_name", "角色名称须为 1–24 个字符且不能包含控制字符")
		return
	}
	result, err := s.db.ExecContext(
		r.Context(),
		`UPDATE monitor_sessions SET role_name = ?
		 WHERE user_id = ? AND client_id = ? AND status = 1`,
		request.RoleName, mustUser(r.Context()).ID, request.ClientID,
	)
	if err != nil {
		s.internalError(w, "save role name failed", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "client_not_found", "客户端不存在或已解绑")
		return
	}
	s.hub.NotifyClients(mustUser(r.Context()).ID)
	writeJSON(w, http.StatusOK, map[string]any{"roleName": request.RoleName})
}

func (s *Server) handleSaveRopeTeam(w http.ResponseWriter, r *http.Request) {
	var request struct {
		LeaderSessionID  string   `json:"leaderSessionId"`
		MemberSessionIDs []string `json:"memberSessionIds"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.MemberSessionIDs) < 1 || len(request.MemberSessionIDs) > 5 {
		writeError(w, http.StatusBadRequest, "invalid_team_size", "队伍必须选择 1–5 个客户端")
		return
	}
	seen := make(map[string]bool, len(request.MemberSessionIDs))
	leaderSelected := false
	for _, sessionID := range request.MemberSessionIDs {
		if len(sessionID) != 36 || seen[sessionID] {
			writeError(w, http.StatusBadRequest, "invalid_team_members", "队伍客户端选择无效或重复")
			return
		}
		seen[sessionID] = true
		leaderSelected = leaderSelected || sessionID == request.LeaderSessionID
	}
	if !leaderSelected {
		writeError(w, http.StatusBadRequest, "leader_not_selected", "队长必须是已选择的客户端")
		return
	}

	userID := mustUser(r.Context()).ID
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		s.internalError(w, "begin rope team save failed", err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	roleNames := make(map[string]string, len(request.MemberSessionIDs))
	usedRoleNames := make(map[string]bool, len(request.MemberSessionIDs))
	for _, sessionID := range request.MemberSessionIDs {
		online, _, _ := s.hub.ClientStatus(sessionID)
		if !online {
			writeError(w, http.StatusConflict, "team_member_offline", "保存并执行队伍配置时，所有成员客户端都必须在线")
			return
		}
		var roleName string
		err := tx.QueryRowContext(
			r.Context(),
			`SELECT role_name FROM monitor_sessions
			 WHERE id = ? AND user_id = ? AND status = 1 LIMIT 1`,
			sessionID, userID,
		).Scan(&roleName)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "client_not_found", "所选客户端不存在或已解绑")
			return
		}
		if err != nil {
			s.internalError(w, "validate rope team member failed", err)
			return
		}
		if !validRoleName(roleName) {
			writeError(w, http.StatusBadRequest, "role_name_required", "所有队伍成员都必须先填写角色名称")
			return
		}
		if usedRoleNames[roleName] {
			writeError(w, http.StatusBadRequest, "duplicate_role_name", "队伍成员的角色名称不能重复")
			return
		}
		usedRoleNames[roleName] = true
		roleNames[sessionID] = roleName
	}

	var teamID int64
	firstCreation := false
	err = tx.QueryRowContext(
		r.Context(),
		`SELECT id FROM rope_teams WHERE user_id = ? FOR UPDATE`,
		userID,
	).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.ExecContext(
			r.Context(),
			`INSERT INTO rope_teams (user_id, leader_session_id) VALUES (?, ?)`,
			userID, request.LeaderSessionID,
		)
		if insertErr != nil {
			s.internalError(w, "create rope team failed", insertErr)
			return
		}
		teamID, err = result.LastInsertId()
		firstCreation = true
	} else if err == nil {
		_, err = tx.ExecContext(
			r.Context(),
			`UPDATE rope_teams
			 SET leader_session_id = ?, created_in_game = 0, boss_cycle_state = 'idle'
			 WHERE id = ?`,
			request.LeaderSessionID, teamID,
		)
	} else {
		s.internalError(w, "load rope team for update failed", err)
		return
	}
	if err != nil {
		s.internalError(w, "update rope team failed", err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM rope_team_members WHERE team_id = ?`, teamID); err != nil {
		s.internalError(w, "replace rope team members failed", err)
		return
	}
	for _, sessionID := range request.MemberSessionIDs {
		joined := false
		if _, err = tx.ExecContext(
			r.Context(),
			`INSERT INTO rope_team_members (team_id, session_id, invited, joined, joined_at)
			 VALUES (?, ?, 0, ?, IF(?, NOW(3), NULL))`,
			teamID, sessionID, joined, joined,
		); err != nil {
			s.internalError(w, "insert rope team member failed", err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		s.internalError(w, "commit rope team failed", err)
		return
	}

	inviteRoleNames := make([]string, 0, len(request.MemberSessionIDs)-1)
	for _, sessionID := range request.MemberSessionIDs {
		if sessionID != request.LeaderSessionID {
			inviteRoleNames = append(inviteRoleNames, roleNames[sessionID])
		}
	}
	for _, sessionID := range request.MemberSessionIDs {
		command := protocol.ClientCommand{
			Type:     "command",
			Action:   "configure_rope_party",
			TeamID:   teamID,
			IsLeader: sessionID == request.LeaderSessionID,
			// “保存并执行”始终按当前网页配置重建游戏内队伍，
			// 这样创建、邀请和入队状态都能由本轮客户端回执准确推进。
			FirstCreation: true,
			RoleName:      roleNames[sessionID],
		}
		if command.IsLeader {
			command.InviteRoleNames = inviteRoleNames
		}
		s.hub.SendCommand(sessionID, command)
	}
	s.hub.NotifyClients(userID)
	team, loadErr := s.ropeTeam(r.Context(), userID)
	if loadErr != nil {
		s.internalError(w, "reload rope team failed", loadErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"team":          team,
		"firstCreation": firstCreation,
	})
}

func (s *Server) handleDeleteRopeTeam(w http.ResponseWriter, r *http.Request) {
	userID := mustUser(r.Context()).ID
	var teamID int64
	var leaderSessionID string
	err := s.db.QueryRowContext(
		r.Context(),
		`SELECT id, leader_session_id FROM rope_teams WHERE user_id = ? LIMIT 1`,
		userID,
	).Scan(&teamID, &leaderSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "rope_team_not_found", "当前账号还没有挂绳队伍")
		return
	}
	if err != nil {
		s.internalError(w, "load rope team for delete failed", err)
		return
	}
	result, err := s.db.ExecContext(
		r.Context(),
		`DELETE FROM rope_teams WHERE id = ? AND user_id = ?`,
		teamID, userID,
	)
	if err != nil {
		s.internalError(w, "delete rope team failed", err)
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		writeError(w, http.StatusNotFound, "rope_team_not_found", "当前账号还没有挂绳队伍")
		return
	}

	// Exiting the in-game party is best-effort. A later team creation always
	// starts with /退出隊伍, so an offline leader must not block deleting the
	// web-side team configuration.
	commandSent := s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
		Type:   "command",
		Action: "disband_rope_party",
		TeamID: teamID,
	})
	s.hub.NotifyClients(userID)
	writeJSON(w, http.StatusOK, map[string]any{
		"teamId":      teamID,
		"commandSent": commandSent,
	})
}

func (s *Server) handleRemoveRopeTeamMember(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionId"))
	if len(sessionID) != 36 {
		writeError(w, http.StatusBadRequest, "invalid_session_id", "队伍成员编号无效")
		return
	}

	userID := mustUser(r.Context()).ID
	var teamID int64
	var leaderSessionID string
	var roleName string
	err := s.db.QueryRowContext(
		r.Context(),
		`SELECT rt.id, rt.leader_session_id, ms.role_name
		 FROM rope_teams rt
		 JOIN rope_team_members rtm ON rtm.team_id = rt.id
		 JOIN monitor_sessions ms ON ms.id = rtm.session_id
		 WHERE rt.user_id = ? AND rtm.session_id = ? AND ms.status = 1
		 LIMIT 1`,
		userID, sessionID,
	).Scan(&teamID, &leaderSessionID, &roleName)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "rope_team_member_not_found", "该成员不在当前挂绳队伍中")
		return
	}
	if err != nil {
		s.internalError(w, "load rope team member for removal failed", err)
		return
	}
	if sessionID == leaderSessionID {
		writeError(w, http.StatusBadRequest, "cannot_remove_team_leader", "不能直接移除队长，请先修改队长或解散队伍")
		return
	}
	if !validRoleName(roleName) {
		writeError(w, http.StatusConflict, "invalid_member_role_name", "成员角色名称无效，无法发送踢出指令")
		return
	}
	if online, _, _ := s.hub.ClientStatus(leaderSessionID); !online {
		writeError(w, http.StatusConflict, "team_leader_offline", "队长客户端必须在线才能移除成员")
		return
	}
	if !s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
		Type:           "command",
		Action:         "remove_rope_party_member",
		TeamID:         teamID,
		TargetRoleName: roleName,
	}) {
		writeError(w, http.StatusConflict, "team_leader_unavailable", "队长客户端暂时无法接收移除指令，请稍后重试")
		return
	}

	result, err := s.db.ExecContext(
		r.Context(),
		`DELETE rtm FROM rope_team_members rtm
		 JOIN rope_teams rt ON rt.id = rtm.team_id
		 WHERE rtm.team_id = ? AND rtm.session_id = ? AND rt.user_id = ?`,
		teamID, sessionID, userID,
	)
	if err != nil {
		s.internalError(w, "remove rope team member failed", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "rope_team_member_not_found", "该成员不在当前挂绳队伍中")
		return
	}
	s.hub.NotifyClients(userID)
	team, loadErr := s.ropeTeam(r.Context(), userID)
	if loadErr != nil {
		s.internalError(w, "reload rope team after member removal failed", loadErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team})
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	items, err := s.clientItems(r.Context(), mustUser(r.Context()).ID)
	if err != nil {
		s.internalError(w, "list clients failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": items})
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionId"))
	if len(sessionID) != 36 {
		writeError(w, http.StatusBadRequest, "invalid_session_id", "客户端记录编号无效")
		return
	}
	userID := mustUser(r.Context()).ID
	removed, err := s.revokeClient(r.Context(), userID, sessionID)
	if err != nil {
		s.internalError(w, "delete client failed", err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "client_not_found", "客户端不存在或已解绑")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeClient(ctx context.Context, userID int64, sessionID string) (bool, error) {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE monitor_sessions
		 SET status = 0, running = 0, revoked_at = NOW(3)
		 WHERE id = ? AND user_id = ? AND client_id IS NOT NULL AND status = 1`,
		sessionID, userID,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	s.hub.DisconnectDevice(sessionID)
	s.hub.NotifyClients(userID)
	return true, nil
}

type clientSocketRequest struct {
	Type     string `json:"type"`
	ClientID string `json:"clientId"`
	Action   string `json:"action"`
}

func (s *Server) handleClientsWebSocket(w http.ResponseWriter, r *http.Request) {
	rawToken := strings.TrimSpace(r.URL.Query().Get("access_token"))
	user, ok := s.authenticateToken(w, r, rawToken)
	if !ok {
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(4 * 1024)

	observerID, _ := randomUUID()
	updates, unsubscribe := s.hub.SubscribeClients(user.ID, observerID)
	defer unsubscribe()

	type readResult struct {
		body []byte
		err  error
	}
	incoming := make(chan readResult)
	go func() {
		for {
			_, body, err := connection.Read(r.Context())
			select {
			case incoming <- readResult{body: body, err: err}:
			case <-r.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	writeClients := func() error {
		items, err := s.clientItems(r.Context(), user.ID)
		if err != nil {
			return err
		}
		team, err := s.ropeTeam(r.Context(), user.ID)
		if err != nil {
			return err
		}
		body, err := json.Marshal(map[string]any{
			"type": "clients", "clients": items, "ropeTeam": team,
		})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		return connection.Write(ctx, websocket.MessageText, body)
	}

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case _, ok := <-updates:
			if !ok || writeClients() != nil {
				return
			}
		case result := <-incoming:
			if result.err != nil {
				return
			}
			var request clientSocketRequest
			if json.Unmarshal(result.body, &request) != nil ||
				request.Type != "command" ||
				(request.Action != "start" && request.Action != "stop") {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid command")
				return
			}
			var sessionID string
			err := s.db.QueryRowContext(
				r.Context(),
				`SELECT id FROM monitor_sessions
				 WHERE user_id = ? AND client_id = ? AND status = 1 LIMIT 1`,
				user.ID, request.ClientID,
			).Scan(&sessionID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return
			}
			s.hub.SendCommand(sessionID, protocol.ClientCommand{
				Type:   "command",
				Action: request.Action,
			})
		case <-pingTicker.C:
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			err := connection.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
