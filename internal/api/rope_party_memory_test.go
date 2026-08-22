package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"autobuff-monitor/server/internal/protocol"
	"autobuff-monitor/server/internal/realtime"
)

func TestConcurrentBuffDueReportsMergeIntoOneEvent(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	server.rope.teams[42] = &memoryRopeTeam{
		ID: 7, LeaderSessionID: "leader", BossRoleName: "boss", State: "ready",
		Members: map[string]*memoryRopeMember{
			"leader": {SessionID: "leader", IsLeader: true, Joined: true},
			"member": {SessionID: "member", Joined: true},
		},
	}

	var reports sync.WaitGroup
	reports.Add(2)
	for sessionID, messageID := range map[string]string{"leader": "message-1", "member": "message-2"} {
		go func() {
			defer reports.Done()
			server.handleMemoryRopeEvent(context.Background(), 42, sessionID, protocol.RopePartyProgressPayload{
				TeamID: 7, Event: "buff_due", MessageID: messageID,
			})
		}()
	}
	reports.Wait()

	server.rope.mu.Lock()
	cycle := server.rope.teams[42].Cycle
	defer server.rope.mu.Unlock()
	if cycle == nil || cycle.ID == "" || cycle.State != "inviting" {
		t.Fatalf("buff_due reports did not merge into one active event: cycle=%+v", cycle)
	}
}

func TestInitialPartyInvitesBossAfterEveryMemberJoins(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	team := testRopeTeam(nil)
	team.State = "setting_up"
	team.BuffAfterSetup = true
	team.SetupAttemptID = "initial-setup-attempt"
	team.Members["leader"].Joined = false
	server.rope.teams[42] = team

	server.handleMemoryRopeEvent(context.Background(), 42, "leader", protocol.RopePartyProgressPayload{
		TeamID: 7, Event: "party_commands_finished", MessageID: "message-1",
	})
	if team.State != "waiting_members" || !team.Members["leader"].Joined || team.Cycle != nil {
		t.Fatalf("initial party did not wait for fixed members: %+v", team)
	}

	server.handleMemoryRopeEvent(context.Background(), 42, "member", protocol.RopePartyProgressPayload{
		TeamID: 7, Event: "team_joined", MessageID: "message-2",
	})
	if team.Cycle == nil || team.Cycle.State != "inviting" || team.Cycle.ID == "" || team.BuffAfterSetup || team.SetupAttemptID != "" || team.State != "ready" {
		t.Fatalf("initial party did not start its boss BUFF event: %+v", team)
	}
}

func TestManualRestartInvitesBossAfterRebuiltMembersJoin(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	cycle := newMemoryBossCycle(true, true, "pre_rebuilding")
	team := testRopeTeam(cycle)
	team.State = "setting_up"
	team.SetupAttemptID = cycle.ID
	team.Members["leader"].Joined = false
	server.rope.teams[42] = team

	server.handleMemoryRopeEvent(context.Background(), 42, "leader", protocol.RopePartyProgressPayload{
		TeamID: 7, EventID: cycle.ID, Event: "rebuild_finished", MessageID: "message-1",
	})
	if cycle.State != "waiting_members" || !team.Members["leader"].Joined {
		t.Fatalf("manual restart did not wait for rebuilt members: %+v", team)
	}

	server.handleMemoryRopeEvent(context.Background(), 42, "member", protocol.RopePartyProgressPayload{
		TeamID: 7, EventID: cycle.ID, Event: "team_joined", MessageID: "message-2",
	})
	if team.Cycle != cycle || cycle.State != "inviting" || cycle.RestartBeforeBuff || team.SetupAttemptID != "" || team.State != "ready" {
		t.Fatalf("manual restart did not continue to the boss BUFF event: %+v", team)
	}
}

func TestStaleTeamTimeoutCannotFailNewSetupAttempt(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	team := testRopeTeam(nil)
	team.State = "waiting_members"
	team.SetupAttemptID = "old-setup-attempt"
	server.rope.teams[42] = team

	server.scheduleRopeTeamTimeout(42, team.ID, team.SetupAttemptID, "waiting_members", time.Millisecond)
	server.rope.mu.Lock()
	team.SetupAttemptID = "new-setup-attempt"
	server.rope.mu.Unlock()
	time.Sleep(10 * time.Millisecond)

	server.rope.mu.Lock()
	state, lastError := team.State, team.LastError
	server.rope.mu.Unlock()
	if state != "waiting_members" || lastError != "" {
		t.Fatalf("stale setup timeout failed the current attempt: state=%s lastError=%q", state, lastError)
	}
}

func TestCurrentTeamTimeoutStillFailsMatchingSetupAttempt(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	team := testRopeTeam(nil)
	team.State = "waiting_members"
	team.SetupAttemptID = "current-setup-attempt"
	server.rope.teams[42] = team

	server.scheduleRopeTeamTimeout(42, team.ID, team.SetupAttemptID, "waiting_members", time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	server.rope.mu.Lock()
	state, lastError := team.State, team.LastError
	server.rope.mu.Unlock()
	if state != "error" || lastError == "" {
		t.Fatalf("current setup timeout did not fail its matching attempt: state=%s lastError=%q", state, lastError)
	}
}

func TestDuplicateBossJoinedDoesNotResetDispatchState(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	cycle := newMemoryBossCycle(false, false, "inviting")
	server.rope.teams[42] = testRopeTeam(cycle)
	progress := protocol.RopePartyProgressPayload{TeamID: 7, EventID: cycle.ID, Event: "boss_joined", MessageID: "message-1"}

	server.handleMemoryRopeEvent(context.Background(), 42, "leader", progress)
	cycle.CommandAcked["leader"] = true
	server.handleMemoryRopeEvent(context.Background(), 42, "leader", progress)

	if cycle.State != "dispatching_buffs" || !cycle.CommandAcked["leader"] {
		t.Fatal("duplicate boss_joined reset an event that was already dispatching")
	}
}

func TestAllMembersAcknowledgedRequiresEveryConfiguredMember(t *testing.T) {
	team := &memoryRopeTeam{
		LeaderSessionID: "leader",
		Members: map[string]*memoryRopeMember{
			"leader":   {SessionID: "leader", IsLeader: true},
			"member-a": {SessionID: "member-a"},
			"member-b": {SessionID: "member-b"},
		},
	}
	cycle := newMemoryBossCycle(false, false, "dispatching_buffs")
	cycle.CommandAcked["leader"] = true
	cycle.CommandAcked["member-a"] = true
	if allMembersAcknowledged(team, cycle) {
		t.Fatal("all members acknowledged while one member was still pending")
	}
	cycle.CommandAcked["member-b"] = true
	if !allMembersAcknowledged(team, cycle) {
		t.Fatal("all members were not recognized after every ACK")
	}
}

func TestCycleCompletionWaitsOnlyForAcknowledgedParticipants(t *testing.T) {
	cycle := newMemoryBossCycle(false, false, "casting")
	cycle.Participants["leader"] = true
	cycle.Participants["member-a"] = true
	cycle.CommandTimedOut["member-b"] = true
	cycle.Completed["leader"] = true
	if allCycleCompleted(cycle) {
		t.Fatal("cycle completed while an acknowledged member was pending")
	}
	cycle.Completed["member-a"] = true
	if !allCycleCompleted(cycle) {
		t.Fatal("a command-timeout member incorrectly blocked completion")
	}
}

func TestRopeEventIDsAreUniqueUUIDs(t *testing.T) {
	first, second := newRopeEventID(), newRopeEventID()
	if first == second || len(first) != 36 || len(second) != 36 {
		t.Fatalf("invalid event IDs: %q %q", first, second)
	}
}

func TestTombstoneRetainsCompletedEvent(t *testing.T) {
	manager := newRopePartyManager()
	manager.addTombstoneLocked("event-123")
	if !manager.hasTombstoneLocked("event-123") {
		t.Fatal("completed event tombstone was not retained")
	}
}

func TestAllCycleCompletedRejectsEmptyBarrier(t *testing.T) {
	cycle := newMemoryBossCycle(false, false, "casting")
	if allCycleCompleted(cycle) {
		t.Fatal("empty completion barrier must not complete")
	}
}

func TestFinalizeCastParticipantsExcludesMissingNonLeader(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	cycle := newMemoryBossCycle(false, false, "dispatching_buffs")
	cycle.CommandAcked["leader"] = true
	server.rope.teams[42] = testRopeTeam(cycle)

	server.finalizeCastParticipants(42, 7, cycle.ID)

	if cycle.State != "casting" || !cycle.Participants["leader"] || cycle.Participants["member"] || !cycle.CommandTimedOut["member"] {
		t.Fatalf("unexpected participant barrier: %+v", cycle)
	}
}

func TestFinalizeCastParticipantsEndsEventWhenLeaderMissing(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	cycle := newMemoryBossCycle(false, false, "dispatching_buffs")
	cycle.CommandAcked["member"] = true
	server.rope.teams[42] = testRopeTeam(cycle)

	server.finalizeCastParticipants(42, 7, cycle.ID)

	team := server.rope.teams[42]
	if team.Cycle != nil || team.State != "error" || !server.rope.hasTombstoneLocked(cycle.ID) {
		t.Fatalf("missing leader ACK did not terminate the event: %+v", team)
	}
}

func TestLateTeamJoinForCompletedEventStillAdvancesRebuild(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	team := testRopeTeam(nil)
	team.State = "waiting_members"
	team.LastEventID = "event-completed"
	team.Members["leader"].Joined = true
	server.rope.teams[42] = team
	server.rope.addTombstoneLocked(team.LastEventID)

	server.handleMemoryRopeEvent(context.Background(), 42, "member", protocol.RopePartyProgressPayload{
		TeamID: 7, EventID: team.LastEventID, Event: "team_joined", MessageID: "message-1",
	})

	if !team.Members["member"].Joined || team.State != "ready" {
		t.Fatalf("late member join was discarded by the event tombstone: %+v", team)
	}
}

func TestDuplicateRebuildFinishedDoesNotReopenCompletedEvent(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	cycle := newMemoryBossCycle(false, false, "rebuilding")
	team := testRopeTeam(cycle)
	server.rope.teams[42] = team
	progress := protocol.RopePartyProgressPayload{TeamID: 7, EventID: cycle.ID, Event: "rebuild_finished", MessageID: "message-1"}

	server.handleMemoryRopeEvent(context.Background(), 42, "leader", progress)
	server.handleMemoryRopeEvent(context.Background(), 42, "leader", progress)

	if team.Cycle != nil || team.LastEventID != cycle.ID || team.State != "waiting_members" {
		t.Fatalf("duplicate rebuild completion changed the completed state: %+v", team)
	}
}

func TestRetryDeadlineDoesNotFailAdvancedEvent(t *testing.T) {
	hub := realtime.NewHub()
	defer hub.Close()
	server := &Server{hub: hub, rope: newRopePartyManager()}
	cycle := newMemoryBossCycle(false, false, "casting")
	team := testRopeTeam(cycle)
	server.rope.teams[42] = team

	server.retryRopeCommand(42, 7, cycle.ID, "leader", "invite_boss", time.Millisecond, func(cycle *memoryBossCycle) bool {
		return cycle.State != "inviting"
	}, protocol.ClientCommand{})

	if team.Cycle != cycle || team.State == "error" {
		t.Fatal("an obsolete retry deadline terminated an event after it advanced")
	}
}

func TestRopeTeamViewCopiesCycleState(t *testing.T) {
	manager := newRopePartyManager()
	cycle := newMemoryBossCycle(false, false, "casting")
	cycle.Completed["leader"] = false
	manager.teams[42] = testRopeTeam(cycle)

	view := manager.view(42, func(string) bool { return true })
	cycle.Completed["leader"] = true
	cycle.State = "notifying_leader"

	if view.Cycle.State != "casting" || view.Cycle.Completed["leader"] {
		t.Fatal("rope team view retained mutable cycle state")
	}
}

func testRopeTeam(cycle *memoryBossCycle) *memoryRopeTeam {
	return &memoryRopeTeam{
		ID: 7, LeaderSessionID: "leader", BossRoleName: "boss", State: "ready", Cycle: cycle,
		Members: map[string]*memoryRopeMember{
			"leader": {SessionID: "leader", IsLeader: true, Joined: true},
			"member": {SessionID: "member", Joined: false},
		},
	}
}
