package api

import "testing"

func TestBeginCycleCastingWaitsForEveryTeamMember(t *testing.T) {
	team := &memoryRopeTeam{
		LeaderSessionID: "leader",
		Members: map[string]*memoryRopeMember{
			"leader":   {SessionID: "leader", IsLeader: true},
			"member-a": {SessionID: "member-a"},
			"member-b": {SessionID: "member-b"},
		},
	}
	cycle := &memoryBossCycle{
		State:        "inviting",
		Participants: map[string]bool{"leader": true},
		Completed:    map[string]bool{"leader": true},
	}

	memberIDs := beginCycleCastingLocked(team, cycle)
	if len(memberIDs) != 3 || len(cycle.Participants) != 3 {
		t.Fatalf("casting barrier contains %d members, want 3", len(cycle.Participants))
	}
	if allCycleCompleted(cycle) {
		t.Fatal("cycle completed before members reported")
	}
	cycle.Completed["leader"] = true
	cycle.Completed["member-a"] = true
	if allCycleCompleted(cycle) {
		t.Fatal("cycle completed while one member was still pending")
	}
	cycle.Completed["member-b"] = true
	if !allCycleCompleted(cycle) {
		t.Fatal("cycle did not complete after every member reported")
	}
}

func TestAllCycleCompletedRejectsEmptyBarrier(t *testing.T) {
	cycle := &memoryBossCycle{Participants: map[string]bool{}, Completed: map[string]bool{}}
	if allCycleCompleted(cycle) {
		t.Fatal("empty completion barrier must not complete")
	}
}
