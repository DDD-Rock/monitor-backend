package api

import "testing"

func TestValidRegistrationInviteCode(t *testing.T) {
	t.Parallel()

	valid := []string{
		"XIAOXIN",
		"xiaoxin",
		"XiaoXin",
		"  xIaOxIn  ",
	}
	for _, value := range valid {
		if !validRegistrationInviteCode(value) {
			t.Fatalf("expected invite code %q to be valid", value)
		}
	}

	invalid := []string{"", "XIAOXIN1", "XIAO XIN", "OTHER"}
	for _, value := range invalid {
		if validRegistrationInviteCode(value) {
			t.Fatalf("expected invite code %q to be invalid", value)
		}
	}
}
