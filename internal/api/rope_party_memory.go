package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"autobuff-monitor/server/internal/protocol"
)

var errRopeTeamNotFound = errors.New("rope team not found")

const (
	ropeCommandAckWindow = 5 * time.Second
	ropeCastingTimeout   = 45 * time.Second
	ropeInviteTimeout    = 90 * time.Second
	ropeRebuildTimeout   = 45 * time.Second
	ropeEventCooldown    = 20 * time.Second
	ropeTombstoneTTL     = 5 * time.Minute
)

type ropePartyManager struct {
	mu         sync.Mutex
	teams      map[int64]*memoryRopeTeam
	tombstones map[string]time.Time
	nextTeamID atomic.Int64
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
	ID                string          `json:"id"`
	State             string          `json:"state"`
	Manual            bool            `json:"manual"`
	RestartBeforeBuff bool            `json:"-"`
	Participants      map[string]bool `json:"-"`
	CommandAcked      map[string]bool `json:"-"`
	Completed         map[string]bool `json:"-"`
	CommandTimedOut   map[string]bool `json:"-"`
	CastTimedOut      map[string]bool `json:"-"`
	InviteAcked       bool            `json:"-"`
	LeaderReadyAcked  bool            `json:"-"`
	RebuildAcked      bool            `json:"-"`
	InviteStatus      string          `json:"inviteStatus,omitempty"`
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
	LastEventID     string                       `json:"-"`
	LastCompletedAt int64                        `json:"-"`
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
	m := &ropePartyManager{teams: make(map[int64]*memoryRopeTeam), tombstones: make(map[string]time.Time)}
	m.nextTeamID.Store(time.Now().UnixMilli())
	return m
}

func (m *ropePartyManager) view(userID int64, online func(string) bool) *memoryRopeTeamView {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[userID]
	if team == nil {
		return nil
	}
	view := &memoryRopeTeamView{ID: team.ID, LeaderSessionID: team.LeaderSessionID, BossRoleName: team.BossRoleName, State: team.State, LastError: team.LastError, UpdatedAt: team.UpdatedAt}
	if team.Cycle != nil {
		cycle := *team.Cycle
		cycle.Participants = cloneBoolMap(team.Cycle.Participants)
		cycle.CommandAcked = cloneBoolMap(team.Cycle.CommandAcked)
		cycle.Completed = cloneBoolMap(team.Cycle.Completed)
		cycle.CommandTimedOut = cloneBoolMap(team.Cycle.CommandTimedOut)
		cycle.CastTimedOut = cloneBoolMap(team.Cycle.CastTimedOut)
		view.Cycle = &cycle
	}
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

func cloneBoolMap(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
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
	s.scheduleRopeTeamTimeout(userID, team.ID, "setting_up", 30*time.Second)
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
	// Recovery deliberately supersedes a stalled event with a fresh UUID. Late
	// messages from the old event are acknowledged through the tombstone map but
	// can no longer advance this team.
	if team.Cycle != nil {
		s.rope.addTombstoneLocked(team.Cycle.ID)
	}
	cycle := newMemoryBossCycle(true, true, "pre_rebuilding")
	team.Cycle, team.State, team.LastError = cycle, "setting_up", ""
	for _, member := range team.Members {
		member.Joined, member.Completed = false, false
	}
	leaderID, teamID, bossName, invites := team.LeaderSessionID, team.ID, team.BossRoleName, memberRoleNames(team, false)
	prepareMemberIDs := make([]string, 0, len(team.Members))
	for sessionID := range team.Members {
		if sessionID != leaderID {
			prepareMemberIDs = append(prepareMemberIDs, sessionID)
		}
	}
	s.rope.mu.Unlock()
	if online, _, _ := s.hub.ClientStatus(leaderID); !online {
		s.failMemoryRopeTeam(userID, "队长离线，无法重新组队")
		writeError(w, http.StatusConflict, "leader_offline", "队长离线")
		return
	}
	for _, sessionID := range prepareMemberIDs {
		s.hub.SendCommand(sessionID, protocol.ClientCommand{Type: "command", Action: "prepare_for_rebuild", TeamID: teamID, EventID: cycle.ID})
	}
	if !s.hub.SendCommand(leaderID, protocol.ClientCommand{Type: "command", Action: "restart_party_and_buff", TeamID: teamID, EventID: cycle.ID, TargetRoleName: bossName, InviteRoleNames: invites}) {
		s.failMemoryRopeTeam(userID, "队长无法接收重新组队命令")
	}
	s.hub.NotifyClients(userID)
	go s.retryRopeCommand(userID, teamID, cycle.ID, leaderID, "restart_party_and_buff", ropeRebuildTimeout, func(cycle *memoryBossCycle) bool {
		return cycle.State != "pre_rebuilding" || cycle.RebuildAcked
	}, protocol.ClientCommand{Type: "command", Action: "restart_party_and_buff", TeamID: teamID, EventID: cycle.ID, TargetRoleName: bossName, InviteRoleNames: invites})
	s.scheduleRopeEventTimeout(userID, teamID, cycle.ID, "pre_rebuilding", ropeRebuildTimeout, "队长重新组队超时")
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
	ackEventID := progress.EventID
	startInvite := false
	dispatchBuffs := false
	finalizeParticipants := false
	notifyLeader := false
	prepareMembers := make([]string, 0)
	waitingEventID := ""
	waitingWithoutEvent := false

	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team == nil || team.ID != progress.TeamID {
		s.rope.mu.Unlock()
		s.sendRopeEventAck(sessionID, progress, ackEventID)
		return
	}
	lateRebuildJoin := progress.Event == "team_joined" && team.State == "waiting_members" && progress.EventID == team.LastEventID
	if progress.EventID != "" && s.rope.hasTombstoneLocked(progress.EventID) && !lateRebuildJoin {
		s.rope.mu.Unlock()
		s.sendRopeEventAck(sessionID, progress, progress.EventID)
		return
	}
	cycle := team.Cycle
	notify := true
	switch progress.Event {
	case "party_commands_finished":
		if sessionID == team.LeaderSessionID && team.State == "setting_up" && cycle == nil {
			team.Members[sessionID].Joined = true
			team.State = "waiting_members"
			go s.scheduleRopeTeamTimeout(userID, team.ID, "waiting_members", 45*time.Second)
		}
	case "team_joined":
		if progress.EventID == "" || progress.EventID == team.LastEventID || cycle != nil && progress.EventID == cycle.ID {
			if member := team.Members[sessionID]; member != nil {
				member.Joined = true
			}
		}
	case "buff_due":
		if cycle != nil {
			ackEventID = cycle.ID
		} else if team.State == "ready" && allMembersJoined(team) &&
			time.Now().UnixMilli()-team.LastCompletedAt >= ropeEventCooldown.Milliseconds() {
			cycle = newMemoryBossCycle(false, false, "inviting")
			team.Cycle = cycle
			ackEventID = cycle.ID
			startInvite = true
		}
	case "invite_boss_ack":
		if validRopeEvent(team, progress.EventID, "inviting") && sessionID == team.LeaderSessionID {
			cycle.InviteAcked = true
		}
	case "boss_invite_status":
		// Best-effort UI telemetry only. It never advances the event.
		notify = validRopeEvent(team, progress.EventID, "inviting")
		if notify {
			cycle.InviteStatus = progress.RoleName
		}
	case "boss_joined":
		if validRopeEvent(team, progress.EventID, "inviting") && sessionID == team.LeaderSessionID {
			cycle.State = "dispatching_buffs"
			cycle.CommandAcked = make(map[string]bool, len(team.Members))
			cycle.Participants = make(map[string]bool, len(team.Members))
			cycle.Completed = make(map[string]bool, len(team.Members))
			cycle.CommandTimedOut = make(map[string]bool)
			cycle.CastTimedOut = make(map[string]bool)
			dispatchBuffs = true
		}
	case "cast_buffs_ack":
		if validRopeEventState(team, progress.EventID, "dispatching_buffs", "casting") && team.Members[sessionID] != nil {
			cycle.CommandAcked[sessionID] = true
			if cycle.State == "dispatching_buffs" && allMembersAcknowledged(team, cycle) {
				finalizeParticipants = true
			}
		}
	case "buff_finished":
		if validRopeEventState(team, progress.EventID, "dispatching_buffs", "casting") && team.Members[sessionID] != nil {
			cycle.Completed[sessionID] = true
			if cycle.State == "casting" && cycle.Participants[sessionID] && allCycleCompleted(cycle) {
				notifyLeader = true
			}
		}
	case "all_buffs_finished_ack":
		if validRopeEvent(team, progress.EventID, "notifying_leader") && sessionID == team.LeaderSessionID {
			cycle.LeaderReadyAcked = true
			cycle.State = "rebuilding"
			team.State = "setting_up"
			for memberID, member := range team.Members {
				member.Joined = false
				if memberID != team.LeaderSessionID {
					prepareMembers = append(prepareMembers, memberID)
				}
			}
			go s.scheduleRopeEventTimeout(userID, team.ID, cycle.ID, "rebuilding", ropeRebuildTimeout, "队长重建队伍超时")
		}
	case "rebuild_command_ack":
		if validRopeEvent(team, progress.EventID, "pre_rebuilding") && sessionID == team.LeaderSessionID {
			cycle.RebuildAcked = true
		}
	case "rebuild_finished":
		if cycle != nil && cycle.ID == progress.EventID && sessionID == team.LeaderSessionID {
			switch cycle.State {
			case "pre_rebuilding":
				cycle.State = "waiting_members"
				team.State = "waiting_members"
				team.Members[sessionID].Joined = true
				waitingEventID = cycle.ID
			case "rebuilding":
				team.Members[sessionID].Joined = true
				team.State = "waiting_members"
				team.LastEventID = cycle.ID
				team.LastCompletedAt = time.Now().UnixMilli()
				s.rope.addTombstoneLocked(cycle.ID)
				team.Cycle = nil
				cycle = nil
				waitingWithoutEvent = true
			}
		}
	default:
		notify = false
	}
	if allMembersJoined(team) && team.State == "waiting_members" {
		if cycle != nil && cycle.State == "waiting_members" && cycle.RestartBeforeBuff {
			cycle.RestartBeforeBuff = false
			cycle.State = "inviting"
			startInvite = true
		} else if cycle == nil {
			team.State = "ready"
		}
	}
	team.UpdatedAt = time.Now().UnixMilli()
	teamID := team.ID
	leaderID := team.LeaderSessionID
	eventID := ""
	if cycle != nil {
		eventID = cycle.ID
	}
	bossName := team.BossRoleName
	memberIDs := make([]string, 0, len(team.Members))
	for memberID := range team.Members {
		memberIDs = append(memberIDs, memberID)
	}
	s.rope.mu.Unlock()

	s.sendRopeEventAck(sessionID, progress, ackEventID)
	if startInvite {
		s.startInviteBoss(userID, teamID, eventID, leaderID, bossName)
	}
	if dispatchBuffs {
		s.dispatchBuffCommands(userID, teamID, eventID, memberIDs)
	}
	if finalizeParticipants {
		s.finalizeCastParticipants(userID, teamID, eventID)
	}
	if notifyLeader {
		s.beginNotifyLeader(userID, teamID, eventID, false)
	}
	if waitingEventID != "" {
		s.scheduleRopeEventTimeout(userID, teamID, waitingEventID, "waiting_members", 45*time.Second, "等待固定成员重新进队超时")
	}
	if waitingWithoutEvent {
		s.scheduleRopeTeamTimeout(userID, teamID, "waiting_members", 45*time.Second)
	}
	for _, memberID := range prepareMembers {
		s.hub.SendCommand(memberID, protocol.ClientCommand{Type: "command", Action: "prepare_for_rebuild", TeamID: teamID, EventID: progress.EventID})
	}
	if notify {
		s.hub.NotifyClients(userID)
	}
}

func newMemoryBossCycle(manual, restartBeforeBuff bool, state string) *memoryBossCycle {
	return &memoryBossCycle{
		ID: newRopeEventID(), State: state, Manual: manual, RestartBeforeBuff: restartBeforeBuff,
		Participants: map[string]bool{}, CommandAcked: map[string]bool{}, Completed: map[string]bool{},
		CommandTimedOut: map[string]bool{}, CastTimedOut: map[string]bool{}, StartedAt: time.Now().UnixMilli(),
	}
}

func newRopeEventID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000000000")))
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	raw := hex.EncodeToString(bytes)
	return raw[0:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

func (m *ropePartyManager) addTombstoneLocked(eventID string) {
	if eventID == "" {
		return
	}
	now := time.Now()
	for id, expiresAt := range m.tombstones {
		if !expiresAt.After(now) {
			delete(m.tombstones, id)
		}
	}
	m.tombstones[eventID] = now.Add(ropeTombstoneTTL)
}

func (m *ropePartyManager) hasTombstoneLocked(eventID string) bool {
	expiresAt, ok := m.tombstones[eventID]
	if ok && !expiresAt.After(time.Now()) {
		delete(m.tombstones, eventID)
		return false
	}
	return ok
}

func validRopeEvent(team *memoryRopeTeam, eventID, state string) bool {
	return team.Cycle != nil && team.Cycle.ID == eventID && team.Cycle.State == state
}

func validRopeEventState(team *memoryRopeTeam, eventID string, states ...string) bool {
	if team.Cycle == nil || team.Cycle.ID != eventID {
		return false
	}
	for _, state := range states {
		if team.Cycle.State == state {
			return true
		}
	}
	return false
}

func (s *Server) sendRopeEventAck(sessionID string, progress protocol.RopePartyProgressPayload, eventID string) {
	if progress.MessageID == "" {
		return
	}
	s.hub.SendCommand(sessionID, protocol.ClientCommand{
		Type: "command", Action: "rope_event_ack", TeamID: progress.TeamID,
		EventID: eventID, Event: progress.Event, MessageID: progress.MessageID,
	})
}

func (s *Server) startInviteBoss(userID, teamID int64, eventID, leaderID, bossName string) {
	command := protocol.ClientCommand{Type: "command", Action: "invite_boss", TeamID: teamID, EventID: eventID, TargetRoleName: bossName}
	s.hub.SendCommand(leaderID, command)
	s.scheduleRopeEventTimeout(userID, teamID, eventID, "inviting", ropeInviteTimeout, "等待老板进队超时")
	go s.retryRopeCommand(userID, teamID, eventID, leaderID, "invite_boss", ropeInviteTimeout, func(cycle *memoryBossCycle) bool {
		return cycle.State != "inviting" || cycle.InviteAcked
	}, command)
}

func (s *Server) dispatchBuffCommands(userID, teamID int64, eventID string, memberIDs []string) {
	command := protocol.ClientCommand{Type: "command", Action: "cast_buffs", TeamID: teamID, EventID: eventID}
	for _, memberID := range memberIDs {
		s.hub.SendCommand(memberID, command)
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		deadline := time.NewTimer(ropeCommandAckWindow)
		defer deadline.Stop()
		for {
			select {
			case <-ticker.C:
				s.rope.mu.Lock()
				team := s.rope.teams[userID]
				if team == nil || team.ID != teamID || !validRopeEvent(team, eventID, "dispatching_buffs") {
					s.rope.mu.Unlock()
					return
				}
				pending := make([]string, 0)
				for memberID := range team.Members {
					if !team.Cycle.CommandAcked[memberID] {
						pending = append(pending, memberID)
					}
				}
				s.rope.mu.Unlock()
				for _, memberID := range pending {
					s.hub.SendCommand(memberID, command)
				}
			case <-deadline.C:
				s.finalizeCastParticipants(userID, teamID, eventID)
				return
			}
		}
	}()
}

func (s *Server) finalizeCastParticipants(userID, teamID int64, eventID string) {
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team == nil || team.ID != teamID || !validRopeEvent(team, eventID, "dispatching_buffs") {
		s.rope.mu.Unlock()
		return
	}
	cycle := team.Cycle
	for memberID := range team.Members {
		if cycle.CommandAcked[memberID] {
			cycle.Participants[memberID] = true
		} else {
			cycle.CommandTimedOut[memberID] = true
		}
	}
	if !cycle.Participants[team.LeaderSessionID] {
		team.State, team.LastError = "error", "队长未确认放 BUFF 指令，本轮已结束"
		s.rope.addTombstoneLocked(cycle.ID)
		team.LastEventID, team.LastCompletedAt, team.Cycle = cycle.ID, time.Now().UnixMilli(), nil
		team.UpdatedAt = time.Now().UnixMilli()
		s.rope.mu.Unlock()
		s.hub.NotifyClients(userID)
		return
	}
	cycle.State = "casting"
	complete := allCycleCompleted(cycle)
	team.UpdatedAt = time.Now().UnixMilli()
	s.rope.mu.Unlock()
	s.hub.NotifyClients(userID)
	if complete {
		s.beginNotifyLeader(userID, teamID, eventID, false)
		return
	}
	go func() {
		time.Sleep(ropeCastingTimeout)
		s.beginNotifyLeader(userID, teamID, eventID, true)
	}()
}

func (s *Server) beginNotifyLeader(userID, teamID int64, eventID string, force bool) {
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team == nil || team.ID != teamID || !validRopeEvent(team, eventID, "casting") {
		s.rope.mu.Unlock()
		return
	}
	cycle := team.Cycle
	if !force && !allCycleCompleted(cycle) {
		s.rope.mu.Unlock()
		return
	}
	if force {
		for memberID := range cycle.Participants {
			if !cycle.Completed[memberID] {
				cycle.CastTimedOut[memberID] = true
			}
		}
	}
	cycle.State = "notifying_leader"
	leaderID := team.LeaderSessionID
	invites := memberRoleNames(team, false)
	team.UpdatedAt = time.Now().UnixMilli()
	s.rope.mu.Unlock()
	command := protocol.ClientCommand{Type: "command", Action: "all_buffs_finished", TeamID: teamID, EventID: eventID, InviteRoleNames: invites}
	s.hub.SendCommand(leaderID, command)
	go s.retryRopeCommand(userID, teamID, eventID, leaderID, "all_buffs_finished", ropeRebuildTimeout, func(cycle *memoryBossCycle) bool {
		return cycle.State != "notifying_leader" || cycle.LeaderReadyAcked
	}, command)
	s.hub.NotifyClients(userID)
}

func (s *Server) retryRopeCommand(userID, teamID int64, eventID, sessionID, action string, timeout time.Duration, done func(*memoryBossCycle) bool, command protocol.ClientCommand) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			s.rope.mu.Lock()
			team := s.rope.teams[userID]
			finished := team == nil || team.ID != teamID || team.Cycle == nil || team.Cycle.ID != eventID || done(team.Cycle)
			s.rope.mu.Unlock()
			if finished {
				return
			}
			s.hub.SendCommand(sessionID, command)
		case <-deadline.C:
			s.rope.mu.Lock()
			team := s.rope.teams[userID]
			timedOut := team != nil && team.ID == teamID && team.Cycle != nil && team.Cycle.ID == eventID && !done(team.Cycle)
			if timedOut {
				s.failMemoryRopeEventLocked(team, eventID, action+" 超时")
			}
			s.rope.mu.Unlock()
			if timedOut {
				s.hub.NotifyClients(userID)
			}
			return
		}
	}
}

func (s *Server) failMemoryRopeEvent(userID, teamID int64, eventID, message string) {
	s.rope.mu.Lock()
	team := s.rope.teams[userID]
	if team != nil && team.ID == teamID && team.Cycle != nil && team.Cycle.ID == eventID {
		s.failMemoryRopeEventLocked(team, eventID, message)
	}
	s.rope.mu.Unlock()
	s.hub.NotifyClients(userID)
}

func (s *Server) failMemoryRopeEventLocked(team *memoryRopeTeam, eventID, message string) {
	s.rope.addTombstoneLocked(eventID)
	team.LastEventID, team.LastCompletedAt = eventID, time.Now().UnixMilli()
	team.Cycle, team.State, team.LastError, team.UpdatedAt = nil, "error", message, time.Now().UnixMilli()
}

func (s *Server) scheduleRopeEventTimeout(userID, teamID int64, eventID, state string, delay time.Duration, message string) {
	time.AfterFunc(delay, func() {
		s.rope.mu.Lock()
		team := s.rope.teams[userID]
		matches := team != nil && team.ID == teamID && validRopeEvent(team, eventID, state)
		if matches {
			s.failMemoryRopeEventLocked(team, eventID, message)
		}
		s.rope.mu.Unlock()
		if matches {
			s.hub.NotifyClients(userID)
		}
	})
}

func (s *Server) scheduleRopeTeamTimeout(userID, teamID int64, teamState string, delay time.Duration) {
	if userID <= 0 {
		return
	}
	time.AfterFunc(delay, func() {
		s.rope.mu.Lock()
		team := s.rope.teams[userID]
		if team == nil || team.ID != teamID || team.State != teamState {
			s.rope.mu.Unlock()
			return
		}
		team.State, team.LastError, team.UpdatedAt = "error", "挂绳组队阶段超时，请点击重新组队放BUFF", time.Now().UnixMilli()
		s.rope.mu.Unlock()
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
	if len(cycle.Participants) == 0 {
		return false
	}
	for sessionID := range cycle.Participants {
		if !cycle.Completed[sessionID] {
			return false
		}
	}
	return true
}

func allMembersAcknowledged(team *memoryRopeTeam, cycle *memoryBossCycle) bool {
	for sessionID := range team.Members {
		if !cycle.CommandAcked[sessionID] {
			return false
		}
	}
	return len(team.Members) > 0
}
