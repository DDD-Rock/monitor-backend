package api

import (
	"context"
	"database/sql"

	"autobuff-monitor/server/internal/protocol"
)

func (s *Server) startBossBuffCycle(ctx context.Context, userID int64, sessionID string) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
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
	if err != nil || bossRoleName == "" || state != "idle" || createdInGame == 0 {
		return
	}
	members, err := ropeTeamMemberSessionIDs(ctx, tx, teamID)
	if err != nil || len(members) == 0 || !s.allClientsOnline(members) {
		return
	}
	var joinedMembers int
	if err = tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM rope_team_members WHERE team_id = ? AND joined = 1`,
		teamID,
	).Scan(&joinedMembers); err != nil || joinedMembers != len(members) {
		return
	}
	cycleID++
	if _, err = tx.ExecContext(
		ctx,
		`UPDATE rope_teams SET boss_cycle_id = ?, boss_cycle_state = 'inviting' WHERE id = ?`,
		cycleID, teamID,
	); err != nil {
		return
	}
	if _, err = tx.ExecContext(
		ctx,
		`UPDATE rope_team_members SET boss_completed_cycle_id = 0 WHERE team_id = ?`,
		teamID,
	); err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	if !s.hub.SendCommand(leaderSessionID, protocol.ClientCommand{
		Type:           "command",
		Action:         "start_boss_invite_cycle",
		TeamID:         teamID,
		CycleID:        cycleID,
		TargetRoleName: bossRoleName,
	}) {
		_, _ = s.db.ExecContext(
			ctx,
			`UPDATE rope_teams SET boss_cycle_state = 'idle'
			 WHERE id = ? AND boss_cycle_id = ? AND boss_cycle_state = 'inviting'`,
			teamID, cycleID,
		)
	}
	s.hub.NotifyClients(userID)
}

func (s *Server) markBossJoined(ctx context.Context, userID int64, sessionID string, progress protocol.RopePartyProgressPayload) {
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
		   AND boss_cycle_state = 'inviting' FOR UPDATE`,
		progress.TeamID, userID, progress.CycleID,
	).Scan(&leaderSessionID)
	if err != nil || leaderSessionID != sessionID {
		return
	}
	members, err := ropeTeamMemberSessionIDs(ctx, tx, progress.TeamID)
	if err != nil || len(members) == 0 || !s.allClientsOnline(members) {
		return
	}
	if _, err = tx.ExecContext(
		ctx,
		`UPDATE rope_teams SET boss_cycle_state = 'casting'
		 WHERE id = ? AND boss_cycle_id = ?`,
		progress.TeamID, progress.CycleID,
	); err != nil || tx.Commit() != nil {
		return
	}
	for _, memberSessionID := range members {
		s.hub.SendCommand(memberSessionID, protocol.ClientCommand{
			Type:    "command",
			Action:  "cast_boss_buffs",
			TeamID:  progress.TeamID,
			CycleID: progress.CycleID,
		})
	}
	s.hub.NotifyClients(userID)
}

func (s *Server) markBossBuffCompleted(ctx context.Context, userID int64, sessionID string, progress protocol.RopePartyProgressPayload) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	var leaderSessionID, bossRoleName string
	err = tx.QueryRowContext(
		ctx,
		`SELECT leader_session_id, boss_role_name FROM rope_teams
		 WHERE id = ? AND user_id = ? AND boss_cycle_id = ?
		   AND boss_cycle_state = 'casting' FOR UPDATE`,
		progress.TeamID, userID, progress.CycleID,
	).Scan(&leaderSessionID, &bossRoleName)
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
			`UPDATE rope_teams SET boss_cycle_state = 'kicking'
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
			Type:           "command",
			Action:         "kick_boss_from_party",
			TeamID:         progress.TeamID,
			CycleID:        progress.CycleID,
			TargetRoleName: bossRoleName,
		})
	}
	s.hub.NotifyClients(userID)
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

func (s *Server) allClientsOnline(sessionIDs []string) bool {
	for _, sessionID := range sessionIDs {
		if online, _, _ := s.hub.ClientStatus(sessionID); !online {
			return false
		}
	}
	return true
}
