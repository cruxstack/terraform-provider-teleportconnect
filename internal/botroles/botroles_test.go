package botroles

import "testing"

func TestBotWrapperRole(t *testing.T) {
	tests := []struct {
		name      string
		roles     []string
		wantRole  string
		wantIsBot bool
	}{
		{"bot identity", []string{"bot-ci-db"}, "bot-ci-db", true},
		{"normal user single role", []string{"db-reader"}, "", false},
		{"normal user multiple roles", []string{"a", "b"}, "", false},
		{"bot-like among many is not a bot", []string{"bot-x", "other"}, "", false},
		{"no roles", nil, "", false},
		{"empty role", []string{""}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := botWrapperRole(tt.roles)
			if ok != tt.wantIsBot || got != tt.wantRole {
				t.Fatalf("botWrapperRole(%v) = (%q, %v), want (%q, %v)", tt.roles, got, ok, tt.wantRole, tt.wantIsBot)
			}
		})
	}
}
