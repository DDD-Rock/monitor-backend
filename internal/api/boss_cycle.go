package api

import (
	"context"
	"database/sql"

	"autobuff-monitor/server/internal/protocol"
)

func (s *Server) startBossBuffCycle(
	ctx context.Context,
	userID int64,
	sessionID string,
	bossRoleNameOverride ...string,
) (bool, string) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, "storage_error"
	}
	defer func() { _ = tx.Rollback() }()

	var teamID, cycleID int64
	var leaderSessionID, bossRoleName, state string
	var createdInGame uint8
	err = tx.QueryRowContext(
		ctx,
		`SELECT rt.id, rt.leader_session_id, rt.boss_role_name,
		        rt.boss_cycle_id, rt.boss_cycle_state, rt.created_in_game
		 FROM rope_teams rt
		 JOIN rope_team_members rtm ON rtm.team_id = rt.id
		 WHERE rt.user_id = ? AND rtm.session_id = ?
		 FOR UPDATE`,
		userID, sessionID,
	).Scan(&teamID, &leaderSessionID, &bossRoleName, &cycleID, &state, &createdInGame)
	if err != nil {
		return false, "team_not_found"
	}
	if state != "idle" {
		return false, "already_active"
	}
	if createdInGame == 0 {
		return false, "team_not_created"
	}
	if len(bossRoleNameOverride) > 0 {
		bossRoleName = bossRoleNameOverride[0]
	}
	if bossRoleName == "" {
		return false, "boss_name_missing"
	}
	members, err := ropeTeamMemberSessionIDs(ctx, tx, teamID)
	if err != nil || len(members) == 0 {
		return false, "team_members_missing"
	}
	onlineMembers := s.activeTempleClientSessionIDs(members)
	if !containsSessionID(onlineMembers, leaderSessionID) {
		return false, "leader_offline"
	}
	for _, memberSessionID := range onlineMembers {
		var joined uint8
		if err = tx.QueryRowContext(
			ctx,
			`SELECT joined FROM rope_team_members WHERE team_id = ? AND session_id = ?`,
			teamID, memberSessionID,
		).Scan(&joined); err != nil {
			return false, "storage_error"
		}
		if joined == 0 {
			return false, "team_members_not_joined"
		}
	}
	// Freeze every online configured member into this cycle. joined is a UI
	// receipt and may arrive slightly late or be retried after reconnect; it must
	// not decide whether an online Buff client is allowed to skip the cycle.
	participants := onlineMembers
	cycleID++
	if _, err = tx.ExecContext(
		ctx,
		`UPDATE rope_teams
		 SET boss_role_name = ?, boss_cycle_id = ?, boss_cycle_state = 'inviting'
		 WHERE id = ?`,
		bossRoleName, cycleID, teamID,
	); err != nil {
		return false, "storage_error"
	}
	// Members that are offline when the cycle starts are skipped for this cycle,
	// so their missing completion receipt cannot block the leader from kicking.
	if _, err = tx.ExecContext(
		ctx,
		`UPDATE rope_team_members SET boss_completed_cycle_id = ? WHERE team_id = ?`,
		cycleID, teamID,
	); err != nil {
		return false, "storage_error"
	}
	for _, memberSessionID := range participants {
		if _, err = tx.ExecContext(
			ctx,
			`UPDATE rope_team_members SET boss_completed_cycle_id = 0
			 WHERE team_id = ? AND session_id = ?`,
			teamID, memberSessionID,
		); err != nil {
			return false, "storage_error"
		}
	}
	if err = tx.Commit(); err != nil {
		return false, "storage_error"
	}
	commandSent := s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
		Type:           "command",
		Action:         "start_boss_invite_cycle",
		TeamID:         teamID,
		CycleID:        cycleID,
		TargetRoleName: bossRoleName,
	})
	if !commandSent {
		_, _ = s.db.ExecContext(
			ctx,
			`UPDATE rope_teams SET boss_cycle_state = 'idle'
			 WHERE id = ? AND boss_cycle_id = ? AND boss_cycle_state = 'inviting'`,
			teamID, cycleID,
		)
	}
	s.hub.NotifyClients(userID)
	if !commandSent {
		return false, "leader_unavailable"
	}
	return true, "started"
}

func (s *Server) markBossJoined(ctx context.Context, userID int64, sessionID string, progress protocol.RopePartyProgressPayload) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	var leaderSessionID, state string
	err = tx.QueryRowContext(
		ctx,
		`SELECT leader_session_id, boss_cycle_state FROM rope_teams
		 WHERE id = ? AND user_id = ? AND boss_cycle_id = ?
		   AND boss_cycle_state IN ('inviting', 'casting') FOR UPDATE`,
		progress.TeamID, userID, progress.CycleID,
	).Scan(&leaderSessionID, &state)
	if err != nil || leaderSessionID != sessionID {
		return
	}
	participants, err := ropeTeamPendingCycleSessionIDs(ctx, tx, progress.TeamID, progress.CycleID)
	if err != nil || len(participants) == 0 {
		return
	}
	onlineParticipants := s.activeTempleClientSessionIDs(participants)
	if state == "inviting" {
		if _, err = tx.ExecContext(
			ctx,
			`UPDATE rope_teams SET boss_cycle_state = 'casting'
			 WHERE id = ? AND boss_cycle_id = ? AND boss_cycle_state = 'inviting'`,
			progress.TeamID, progress.CycleID,
		); err != nil {
			return
		}
	}
	if err = tx.Commit(); err != nil {
		return
	}
	for _, memberSessionID := range onlineParticipants {
		if s.hub.SendCommand(memberSessionID, protocol.ClientCommand{
			Type:    "command",
			Action:  "cast_boss_buffs",
			TeamID:  progress.TeamID,
			CycleID: progress.CycleID,
		}) {
			continue
		}
		// Keep the member pending when a live command cannot be queued. A
		// repeated boss_joined receipt will retry the command instead of
		// incorrectly allowing the cycle to kick the boss early.
	}
	s.hub.NotifyClients(userID)
}

func (s *Server) markBossBuffCompleted(ctx context.Context, userID int64, sessionID string, progress protocol.RopePartyProgressPayload) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	var leaderSessionID string
	err = tx.QueryRowContext(
		ctx,
		`SELECT leader_session_id FROM rope_teams
		 WHERE id = ? AND user_id = ? AND boss_cycle_id = ?
		   AND boss_cycle_state = 'casting' FOR UPDATE`,
		progress.TeamID, userID, progress.CycleID,
	).Scan(&leaderSessionID)
	if err != nil {
		return
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE rope_team_members rtm
		 JOIN rope_teams rt ON rt.id = rtm.team_id
		 SET rtm.boss_completed_cycle_id = ?
		 WHERE rtm.team_id = ? AND rtm.session_id = ? AND rt.user_id = ?
		   AND rt.boss_cycle_id = ? AND rt.boss_cycle_state = 'casting'`,
		progress.CycleID, progress.TeamID, sessionID, userID, progress.CycleID,
	)
	if err != nil {
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return
	}
	if err = s.skipInactiveBossCycleParticipants(ctx, tx, progress.TeamID, progress.CycleID); err != nil {
		return
	}
	var total, completed int
	err = tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*), SUM(rtm.boss_completed_cycle_id = rt.boss_cycle_id)
		 FROM rope_team_members rtm
		 JOIN rope_teams rt ON rt.id = rtm.team_id
		 WHERE rt.id = ? AND rt.user_id = ?
		 GROUP BY rt.id`,
		progress.TeamID, userID,
	).Scan(&total, &completed)
	if err != nil {
		return
	}
	allCompleted := total > 0 && completed == total
	if allCompleted {
		result, err = tx.ExecContext(
			ctx,
			`UPDATE rope_teams SET boss_cycle_state = 'disbanding'
			 WHERE id = ? AND boss_cycle_id = ? AND boss_cycle_state = 'casting'`,
			progress.TeamID, progress.CycleID,
		)
		if err != nil {
			return
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			allCompleted = false
		}
	}
	if err = tx.Commit(); err != nil {
		return
	}
	if allCompleted {
		s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
			Type:    "command",
			Action:  "disband_boss_party",
			TeamID:  progress.TeamID,
			CycleID: progress.CycleID,
		})
	}
	s.hub.NotifyClients(userID)
}

func (s *Server) resumePendingBossBuffCycle(ctx context.Context, userID int64, sessionID string) {
	var teamID, cycleID int64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT rt.id, rt.boss_cycle_id
		 FROM rope_teams rt
		 JOIN rope_team_members rtm ON rtm.team_id = rt.id
		 WHERE rt.user_id = ? AND rtm.session_id = ?
		   AND rt.boss_cycle_state = 'casting'
		   AND rtm.boss_completed_cycle_id <> rt.boss_cycle_id`,
		userID, sessionID,
	).Scan(&teamID, &cycleID)
	if err != nil {
		return
	}
	s.hub.SendCommand(sessionID, protocol.ClientCommand{
		Type: "command", Action: "cast_boss_buffs", TeamID: teamID, CycleID: cycleID,
	})
}

// reconcileBossBuffCycle retries state-changing commands from the regular
// client-state heartbeat and removes clients that stopped or disconnected from
// the current cycle's completion barrier. Client workers deduplicate commands by
// cycle ID, so repeating them is safe and prevents a transient queue failure
// from leaving the web state permanently stuck.
func (s *Server) reconcileBossBuffCycle(ctx context.Context, userID int64) {
	var teamID, cycleID int64
	var leaderSessionID, bossRoleName, state string
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT id, leader_session_id, boss_role_name, boss_cycle_id, boss_cycle_state
		 FROM rope_teams WHERE user_id = ? AND boss_cycle_state <> 'idle' LIMIT 1`,
		userID,
	).Scan(&teamID, &leaderSessionID, &bossRoleName, &cycleID, &state); err != nil {
		return
	}

	switch state {
	case "inviting":
		if s.isActiveTempleClient(leaderSessionID) {
			s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
				Type: "command", Action: "start_boss_invite_cycle", TeamID: teamID,
				CycleID: cycleID, TargetRoleName: bossRoleName,
			})
		}
	case "casting":
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return
		}
		defer func() { _ = tx.Rollback() }()
		if err = s.skipInactiveBossCycleParticipants(ctx, tx, teamID, cycleID); err != nil {
			return
		}
		pending, err := ropeTeamPendingCycleSessionIDs(ctx, tx, teamID, cycleID)
		if err != nil {
			return
		}
		if len(pending) == 0 {
			result, updateErr := tx.ExecContext(
				ctx,
				`UPDATE rope_teams SET boss_cycle_state = 'disbanding'
				 WHERE id = ? AND boss_cycle_id = ? AND boss_cycle_state = 'casting'`,
				teamID, cycleID,
			)
			if updateErr != nil {
				return
			}
			changed, _ := result.RowsAffected()
			if err = tx.Commit(); err != nil {
				return
			}
			if changed > 0 {
				s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
					Type: "command", Action: "disband_boss_party", TeamID: teamID, CycleID: cycleID,
				})
				s.hub.NotifyClients(userID)
			}
			return
		}
		if err = tx.Commit(); err != nil {
			return
		}
		for _, memberSessionID := range pending {
			s.hub.SendCommand(memberSessionID, protocol.ClientCommand{
				Type: "command", Action: "cast_boss_buffs", TeamID: teamID, CycleID: cycleID,
			})
		}
		s.hub.NotifyClients(userID)
	case "disbanding":
		if s.isActiveTempleClient(leaderSessionID) {
			s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
				Type: "command", Action: "disband_boss_party", TeamID: teamID, CycleID: cycleID,
			})
		}
	}
}

func (s *Server) skipInactiveBossCycleParticipants(ctx context.Context, tx *sql.Tx, teamID, cycleID int64) error {
	pending, err := ropeTeamPendingCycleSessionIDs(ctx, tx, teamID, cycleID)
	if err != nil {
		return err
	}
	for _, memberSessionID := range pending {
		if s.isActiveTempleClient(memberSessionID) {
			continue
		}
		if _, err = tx.ExecContext(
			ctx,
			`UPDATE rope_team_members SET boss_completed_cycle_id = ?
			 WHERE team_id = ? AND session_id = ? AND boss_completed_cycle_id <> ?`,
			cycleID, teamID, memberSessionID, cycleID,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) markBossKicked(ctx context.Context, userID int64, sessionID string, progress protocol.RopePartyProgressPayload) {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE rope_teams
		 SET boss_cycle_state = 'idle'
		 WHERE id = ? AND user_id = ? AND leader_session_id = ?
		   AND boss_cycle_id = ? AND boss_cycle_state = 'kicking'`,
		progress.TeamID, userID, sessionID, progress.CycleID,
	)
	if err == nil {
		if affected, _ := result.RowsAffected(); affected > 0 {
			s.hub.NotifyClients(userID)
		}
	}
}

func ropeTeamMemberSessionIDs(ctx context.Context, tx *sql.Tx, teamID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT session_id FROM rope_team_members WHERE team_id = ?`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		members = append(members, sessionID)
	}
	return members, rows.Err()
}

func ropeTeamPendingCycleSessionIDs(ctx context.Context, tx *sql.Tx, teamID, cycleID int64) ([]string, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT session_id FROM rope_team_members
		 WHERE team_id = ? AND boss_completed_cycle_id <> ?`,
		teamID, cycleID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		members = append(members, sessionID)
	}
	return members, rows.Err()
}

func (s *Server) activeTempleClientSessionIDs(sessionIDs []string) []string {
	onlineSessionIDs := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		if s.isActiveTempleClient(sessionID) {
			onlineSessionIDs = append(onlineSessionIDs, sessionID)
		}
	}
	return onlineSessionIDs
}

func (s *Server) isActiveTempleClient(sessionID string) bool {
	connected, state, _ := s.hub.ClientStatus(sessionID)
	return connected && state.Mode == "temple" && state.Running
}

func containsSessionID(sessionIDs []string, target string) bool {
	for _, sessionID := range sessionIDs {
		if sessionID == target {
			return true
		}
	}
	return false
}
