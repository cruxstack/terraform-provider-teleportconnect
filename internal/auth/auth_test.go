package auth

import (
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string // substring; "" means no error
	}{
		{
			name:    "no proxy address",
			cfg:     Config{UseLocalProfile: true},
			wantErr: "proxy_address is required",
		},
		{
			name:    "no auth mode",
			cfg:     Config{ProxyAddress: "teleport.example.com:443"},
			wantErr: "one of identity_file_path",
		},
		{
			name: "local profile ok",
			cfg:  Config{ProxyAddress: "teleport.example.com:443", UseLocalProfile: true},
		},
		{
			name: "identity file path ok",
			cfg:  Config{ProxyAddress: "teleport.example.com:443", IdentityFilePath: "/tmp/id"},
		},
		{
			name: "identity file data ok",
			cfg:  Config{ProxyAddress: "teleport.example.com:443", IdentityFileData: "PEM"},
		},
		{
			name: "multiple auth modes",
			cfg: Config{
				ProxyAddress:     "teleport.example.com:443",
				UseLocalProfile:  true,
				IdentityFilePath: "/tmp/id",
			},
			wantErr: "multiple auth methods set",
		},
		{
			name:    "whitespace proxy address",
			cfg:     Config{ProxyAddress: "   ", UseLocalProfile: true},
			wantErr: "proxy_address is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("expected no error, got %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestProfileNameFromProxy(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"teleport.example.com:443", "teleport.example.com"},
		{"teleport.example.com", "teleport.example.com"},
		{"[::1]:443", "::1"},
		{"[2001:db8::1]:3025", "2001:db8::1"},
		{"127.0.0.1:443", "127.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := profileNameFromProxy(tt.in); got != tt.want {
				t.Fatalf("profileNameFromProxy(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCredentialsSelectsMode(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"local profile", Config{ProxyAddress: "p:443", UseLocalProfile: true}, false},
		{"identity path", Config{ProxyAddress: "p:443", IdentityFilePath: "/tmp/id"}, false},
		{"identity data", Config{ProxyAddress: "p:443", IdentityFileData: "PEM"}, false},
		{"none", Config{ProxyAddress: "p:443"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := tt.cfg.credentials()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got creds=%v", creds)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(creds) != 1 {
				t.Fatalf("expected exactly one credential, got %d", len(creds))
			}
		})
	}
}
