package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"autobuff-monitor/server/internal/protocol"
)

var errRopeTeamNotFound = errors.New("rope team not found")

type ropePartyManager struct {
	mu          sync.Mutex
	teams       map[int64]*memoryRopeTeam
	nextTeamID  atomic.Int64
	nextCycleID atomic.Int64
}

type memoryRopeMember struct {
	SessionID string `json:"sessionId"`
	ClientID  string `json:"clientId"`
	Name      string `json:"name"`
	RoleName  string `json:"roleName"`
	IsLeader  bool   `json:"isLeader"`
	Joined    bool   `json:"joined"`
	Online    bool   `json:"online"`
	Completed bool   `json:"buffCompleted"`
}

type memoryBossCycle struct {
	ID                int64           `json:"id"`
	State             string          `json:"state"`
	Manual            bool            `json:"manual"`
	RestartBeforeBuff bool            `json:"-"`
	Participants      map[string]bool `json:"-"`
	Completed         map[string]bool `json:"-"`
	StartedAt         int64           `json:"startedAt"`
}

type memoryRopeTeam struct {
	ID              int64                        `json:"id"`
	LeaderSessionID string                       `json:"leaderSessionId"`
	BossRoleName    string                       `json:"bossRoleName"`
	State           string                       `json:"state"`
	LastError       string                       `json:"lastError"`
	Members         map[string]*memoryRopeMember `json:"-"`
	Cycle           *memoryBossCycle             `json:"cycle,omitempty"`
	UpdatedAt       int64                        `json:"updatedAt"`
}

type memoryRopeTeamView struct {
	ID              int64               `json:"id"`
	LeaderSessionID string              `json:"leaderSessionId"`
	BossRoleName    string              `json:"bossRoleName"`
	State           string              `json:"state"`
	LastError       string              `json:"lastError"`
	Members         []*memoryRopeMember `json:"members"`
	Cycle           *memoryBossCycle    `json:"cycle,omitempty"`
	UpdatedAt       int64               `json:"updatedAt"`
}

func newRopePartyManager() *ropePartyManager {
	m := &ropePartyManager{teams: make(map[int64]*memoryRopeTeam)}
	m.nextTeamID.Store(time.Now().UnixMilli())
	m.nextCycleID.Store(time.Now().UnixMilli())
	return m
}

func (m *ropePartyManager) view(userID int64, online func(string) bool) *memoryRopeTeamView {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[userID]
	if team == nil {
		return nil
	}
	view := &memoryRopeTeamView{ID: team.ID, LeaderSessionID: team.LeaderSessionID, BossRoleName: team.BossRoleName, State: team.State, LastError: team.LastError, Cycle: team.Cycle, UpdatedAt: team.UpdatedAt}
	for _, member := range team.Members {
		copy := *member
		copy.Online = online(member.SessionID)
		if team.Cycle != nil {
			copy.Completed = team.Cycle.Completed[member.SessionID]
		}
		view.Members = append(view.Members, &copy)
	}
	return view
}

func (s *Server) ropeTeamView(userID int64) *memoryRopeTeamView {
	return s.rope.view(userID, func(sessionID string) bool {
		online, _, _ := s.hub.ClientStatus(sessionID)
		return online
	})
}

func (s *Server) handleMemoryRopeTeam(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"team": s.ropeTeamView(mustUser(r.Context()).ID)})
}

func (s *Server) handleUpdateMemoryRopeBoss(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RoleName string `json:"roleName"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.RoleName = strings.TrimSpace(request.RoleName)
	if !validRoleName(request.RoleName) {
		writeError(w, http.StatusBadRequest, "invalid_boss_role_name", "老板角色名无效")
		return
	}
	userID := mustUser(r.Context()).ID
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team == nil {
		s.rope.mu.Unlock()
		writeError(w, http.StatusNotFound, "rope_team_not_found", "当前没有挂绳队伍")
		return
	}
	for _, member := range team.Members {
		if member.RoleName == request.RoleName {
			s.rope.mu.Unlock()
			writeError(w, http.StatusBadRequest, "duplicate_boss_role_name", "老板不能与固定成员同名")
			return
		}
	}
	team.BossRoleName, team.UpdatedAt = request.RoleName, time.Now().UnixMilli()
	s.rope.mu.Unlock()
	s.hub.NotifyClients(userID)
	writeJSON(w, http.StatusOK, map[string]any{"team": s.ropeTeamView(userID)})
}

func (s *Server) handleCreateMemoryRopeTeam(w http.ResponseWriter, r *http.Request) {
	var request struct {
		LeaderSessionID  string   `json:"leaderSessionId"`
		MemberSessionIDs []string `json:"memberSessionIds"`
		BossRoleName     string   `json:"bossRoleName"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.BossRoleName = strings.TrimSpace(request.BossRoleName)
	if len(request.MemberSessionIDs) < 1 || len(request.MemberSessionIDs) > 5 || !validRoleName(request.BossRoleName) {
		writeError(w, http.StatusBadRequest, "invalid_rope_team", "请选择 1–5 个客户端并填写有效的老板角色名")
		return
	}
	userID := mustUser(r.Context()).ID
	members := make(map[string]*memoryRopeMember, len(request.MemberSessionIDs))
	leaderFound := false
	roleNames := make(map[string]bool)
	for _, sessionID := range request.MemberSessionIDs {
		if members[sessionID] != nil {
			writeError(w, http.StatusBadRequest, "duplicate_member", "队伍成员不能重复")
			return
		}
		online, _, _ := s.hub.ClientStatus(sessionID)
		if !online {
			writeError(w, http.StatusConflict, "team_member_offline", "创建队伍时所有成员必须在线")
			return
		}
		var item memoryRopeMember
		err := s.db.QueryRowContext(r.Context(), `SELECT id, client_id, name, role_name FROM monitor_sessions WHERE id = ? AND user_id = ? AND status = 1`, sessionID, userID).Scan(&item.SessionID, &item.ClientID, &item.Name, &item.RoleName)
		if err != nil || !validRoleName(item.RoleName) || roleNames[item.RoleName] || item.RoleName == request.BossRoleName {
			writeError(w, http.StatusBadRequest, "invalid_team_member", "成员必须存在、角色名有效且不能重复或与老板同名")
			return
		}
		roleNames[item.RoleName] = true
		item.IsLeader = sessionID == request.LeaderSessionID
		leaderFound = leaderFound || item.IsLeader
		members[sessionID] = &item
	}
	if !leaderFound {
		writeError(w, http.StatusBadRequest, "leader_not_selected", "队长必须属于当前队伍")
		return
	}

	s.rope.mu.Lock()
	if s.rope.teams[userID] != nil {
		s.rope.mu.Unlock()
		writeError(w, http.StatusConflict, "rope_team_exists", "请先解散当前队伍")
		return
	}
	team := &memoryRopeTeam{ID: s.rope.nextTeamID.Add(1), LeaderSessionID: request.LeaderSessionID, BossRoleName: request.BossRoleName, State: "setting_up", Members: members, UpdatedAt: time.Now().UnixMilli()}
	s.rope.teams[userID] = team
	s.rope.mu.Unlock()

	invites := memberRoleNames(team, false)
	for sessionID, member := range members {
		command := protocol.ClientCommand{Type: "command", Action: "configure_rope_party", TeamID: team.ID, IsLeader: member.IsLeader, RoleName: member.RoleName, TargetRoleName: team.BossRoleName}
		if member.IsLeader {
			command.InviteRoleNames = invites
		}
		if !s.hub.SendCommand(sessionID, command) {
			s.failMemoryRopeTeam(userID, "客户端无法接收组队配置")
		}
	}
	s.hub.NotifyClients(userID)
	s.scheduleRopeTimeout(userID, team.ID, 0, "setting_up", "", 30*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"team": s.ropeTeamView(userID)})
}

func memberRoleNames(team *memoryRopeTeam, includeLeader bool) []string {
	result := make([]string, 0, len(team.Members))
	for _, member := range team.Members {
		if includeLeader || !member.IsLeader {
			result = append(result, member.RoleName)
		}
	}
	return result
}

func (s *Server) failMemoryRopeTeam(userID int64, message string) {
	s.rope.mu.Lock()
	if team := s.rope.teams[userID]; team != nil {
		team.State, team.LastError, team.UpdatedAt = "error", message, time.Now().UnixMilli()
	}
	s.rope.mu.Unlock()
	s.hub.NotifyClients(userID)
}

func (s *Server) handleRestartMemoryRopeTeam(w http.ResponseWriter, r *http.Request) {
	userID := mustUser(r.Context()).ID
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team == nil {
		s.rope.mu.Unlock()
		writeError(w, http.StatusNotFound, "rope_team_not_found", "当前没有挂绳队伍")
		return
	}
	if team.Cycle != nil && team.Cycle.Manual {
		s.rope.mu.Unlock()
		writeError(w, http.StatusConflict, "restart_in_progress", "正在重新组队")
		return
	}
	cycle := &memoryBossCycle{ID: s.rope.nextCycleID.Add(1), State: "rebuilding", Manual: true, RestartBeforeBuff: true, Participants: map[string]bool{}, Completed: map[string]bool{}, StartedAt: time.Now().UnixMilli()}
	team.Cycle, team.State, team.LastError = cycle, "setting_up", ""
	for _, member := range team.Members {
		member.Joined, member.Completed = false, false
	}
	leaderID, teamID, bossName, invites := team.LeaderSessionID, team.ID, team.BossRoleName, memberRoleNames(team, false)
	s.rope.mu.Unlock()
	if online, _, _ := s.hub.ClientStatus(leaderID); !online {
		s.failMemoryRopeTeam(userID, "队长离线，无法重新组队")
		writeError(w, http.StatusConflict, "leader_offline", "队长离线")
		return
	}
	for sessionID := range team.Members {
		if sessionID != leaderID {
			s.hub.SendCommand(sessionID, protocol.ClientCommand{Type: "command", Action: "prepare_for_rebuild", TeamID: teamID, CycleID: cycle.ID})
		}
	}
	if !s.hub.SendCommand(leaderID, protocol.ClientCommand{Type: "command", Action: "restart_party_and_buff", TeamID: teamID, CycleID: cycle.ID, TargetRoleName: bossName, InviteRoleNames: invites}) {
		s.failMemoryRopeTeam(userID, "队长无法接收重新组队命令")
	}
	s.hub.NotifyClients(userID)
	s.scheduleRopeTimeout(userID, teamID, cycle.ID, "setting_up", "rebuilding", 30*time.Second)
	writeJSON(w, http.StatusAccepted, map[string]any{"team": s.ropeTeamView(userID)})
}

func (s *Server) handleDeleteMemoryRopeTeam(w http.ResponseWriter, r *http.Request) {
	userID := mustUser(r.Context()).ID
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	delete(s.rope.teams, userID)
	s.rope.mu.Unlock()
	if team == nil {
		writeError(w, http.StatusNotFound, "rope_team_not_found", "当前没有挂绳队伍")
		return
	}
	for sessionID, member := range team.Members {
		action := "clear_rope_party"
		if member.IsLeader {
			action = "disband_rope_party"
		}
		s.hub.SendCommand(sessionID, protocol.ClientCommand{Type: "command", Action: action, TeamID: team.ID})
	}
	s.hub.NotifyClients(userID)
	writeJSON(w, http.StatusOK, map[string]any{"teamId": team.ID})
}

func (s *Server) handleRemoveMemoryRopeMember(w http.ResponseWriter, r *http.Request) {
	userID, sessionID := mustUser(r.Context()).ID, strings.TrimSpace(r.PathValue("sessionId"))
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team == nil || team.Members[sessionID] == nil {
		s.rope.mu.Unlock()
		writeError(w, http.StatusNotFound, "member_not_found", "成员不存在")
		return
	}
	member := team.Members[sessionID]
	if member.IsLeader {
		s.rope.mu.Unlock()
		writeError(w, http.StatusBadRequest, "cannot_remove_leader", "不能直接移除队长")
		return
	}
	delete(team.Members, sessionID)
	team.UpdatedAt = time.Now().UnixMilli()
	leaderID, teamID, roleName := team.LeaderSessionID, team.ID, member.RoleName
	s.rope.mu.Unlock()
	s.hub.SendCommand(leaderID, protocol.ClientCommand{Type: "command", Action: "remove_member", TeamID: teamID, TargetRoleName: roleName})
	s.hub.SendCommand(sessionID, protocol.ClientCommand{Type: "command", Action: "clear_rope_party", TeamID: teamID})
	s.hub.NotifyClients(userID)
	writeJSON(w, http.StatusOK, map[string]any{"team": s.ropeTeamView(userID)})
}

func (s *Server) handleMemoryRopeEvent(_ context.Context, userID int64, sessionID string, progress protocol.RopePartyProgressPayload) {
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team == nil || team.ID != progress.TeamID {
		s.rope.mu.Unlock()
		return
	}
	cycle := team.Cycle
	notify := true
	switch progress.Event {
	case "party_commands_finished":
		if sessionID != team.LeaderSessionID || team.State != "setting_up" {
			break
		}
		team.Members[sessionID].Joined = true
		team.State = "waiting_members"
		go s.scheduleRopeTimeout(userID, team.ID, 0, "waiting_members", "", 45*time.Second)
	case "team_joined":
		if member := team.Members[sessionID]; member != nil {
			member.Joined = true
		}
	case "buff_due":
		if team.State == "ready" && cycle == nil && allMembersJoined(team) {
			cycle = s.newMemoryBossCycleLocked(team, false)
		}
	case "boss_joined":
		if cycle == nil || cycle.ID != progress.CycleID || cycle.State != "inviting" || sessionID != team.LeaderSessionID {
			break
		}
		cycle.State = "casting"
		for memberID := range cycle.Participants {
			s.hub.SendCommand(memberID, protocol.ClientCommand{Type: "command", Action: "cast_buffs", TeamID: team.ID, CycleID: cycle.ID})
		}
		go s.scheduleRopeTimeout(userID, team.ID, cycle.ID, "", "casting", 30*time.Second)
	case "buff_finished":
		if cycle == nil || cycle.ID != progress.CycleID || cycle.State != "casting" || !cycle.Participants[sessionID] {
			break
		}
		cycle.Completed[sessionID] = true
		if allCycleCompleted(cycle) {
			cycle.State = "rebuilding"
			for _, member := range team.Members {
				member.Joined = false
			}
			s.hub.SendCommand(team.LeaderSessionID, protocol.ClientCommand{Type: "command", Action: "rebuild_party", TeamID: team.ID, CycleID: cycle.ID, InviteRoleNames: memberRoleNames(team, false)})
			go s.scheduleRopeTimeout(userID, team.ID, cycle.ID, "", "rebuilding", 30*time.Second)
		}
	case "party_rebuild_commands_finished":
		if cycle == nil || cycle.ID != progress.CycleID || cycle.State != "rebuilding" || sessionID != team.LeaderSessionID {
			break
		}
		team.Members[sessionID].Joined = true
		team.State, cycle.State = "waiting_members", "waiting_members"
		go s.scheduleRopeTimeout(userID, team.ID, cycle.ID, "waiting_members", "waiting_members", 45*time.Second)
	default:
		notify = false
	}
	if allMembersJoined(team) && team.State == "waiting_members" {
		if cycle == nil {
			cycle = s.newMemoryBossCycleLocked(team, false)
		} else if cycle.RestartBeforeBuff {
			cycle.RestartBeforeBuff, cycle.State = false, "inviting"
			s.sendInviteBossLocked(team, cycle)
		} else if cycle.State == "waiting_members" {
			team.Cycle, team.State = nil, "ready"
		}
	}
	team.UpdatedAt = time.Now().UnixMilli()
	s.rope.mu.Unlock()
	if notify {
		s.hub.NotifyClients(userID)
	}
}

func (s *Server) newMemoryBossCycleLocked(team *memoryRopeTeam, manual bool) *memoryBossCycle {
	cycle := &memoryBossCycle{ID: s.rope.nextCycleID.Add(1), State: "inviting", Manual: manual, Participants: map[string]bool{}, Completed: map[string]bool{}, StartedAt: time.Now().UnixMilli()}
	for sessionID, member := range team.Members {
		online, _, _ := s.hub.ClientStatus(sessionID)
		if online && member.Joined {
			cycle.Participants[sessionID] = true
		}
	}
	team.Cycle = cycle
	s.sendInviteBossLocked(team, cycle)
	return cycle
}

func (s *Server) sendInviteBossLocked(team *memoryRopeTeam, cycle *memoryBossCycle) {
	s.hub.SendCommand(team.LeaderSessionID, protocol.ClientCommand{Type: "command", Action: "invite_boss", TeamID: team.ID, CycleID: cycle.ID, TargetRoleName: team.BossRoleName})
	go s.scheduleRopeTimeout(teamUserID(s.rope, team), team.ID, cycle.ID, "", "inviting", 30*time.Second)
}

// teamUserID is only called while the manager lock is held.
func teamUserID(manager *ropePartyManager, target *memoryRopeTeam) int64 {
	for userID, team := range manager.teams {
		if team == target {
			return userID
		}
	}
	return 0
}

func (s *Server) scheduleRopeTimeout(userID, teamID, cycleID int64, teamState, cycleState string, delay time.Duration) {
	if userID <= 0 {
		return
	}
	time.AfterFunc(delay, func() {
		s.rope.mu.Lock()
		team := s.rope.teams[userID]
		if team == nil || team.ID != teamID || (teamState != "" && team.State != teamState) {
			s.rope.mu.Unlock()
			return
		}
		if cycleState != "" && (team.Cycle == nil || team.Cycle.ID != cycleID || team.Cycle.State != cycleState) {
			s.rope.mu.Unlock()
			return
		}
		if cycleState == "casting" && team.Cycle != nil {
			team.Cycle.State = "rebuilding"
			for _, member := range team.Members {
				member.Joined = false
			}
			leaderID, command := team.LeaderSessionID, protocol.ClientCommand{Type: "command", Action: "rebuild_party", TeamID: team.ID, CycleID: team.Cycle.ID, InviteRoleNames: memberRoleNames(team, false)}
			team.LastError, team.UpdatedAt = "部分客户端放 BUFF 超时，已继续重建队伍", time.Now().UnixMilli()
			s.rope.mu.Unlock()
			s.hub.SendCommand(leaderID, command)
			s.scheduleRopeTimeout(userID, teamID, cycleID, "", "rebuilding", 30*time.Second)
		} else {
			team.State, team.LastError, team.UpdatedAt = "error", "挂绳组队阶段超时，请点击重新组队放BUFF", time.Now().UnixMilli()
			s.rope.mu.Unlock()
		}
		s.hub.NotifyClients(userID)
	})
}

func allMembersJoined(team *memoryRopeTeam) bool {
	if len(team.Members) == 0 {
		return false
	}
	for _, member := range team.Members {
		if !member.Joined {
			return false
		}
	}
	return true
}

func allCycleCompleted(cycle *memoryBossCycle) bool {
	for sessionID := range cycle.Participants {
		if !cycle.Completed[sessionID] {
			return false
		}
	}
	return true
}
