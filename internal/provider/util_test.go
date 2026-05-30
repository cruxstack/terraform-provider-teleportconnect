package provider

import (
	"testing"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/tunnel"
)

func TestStringOrDefault(t *testing.T) {
	if got := stringOrDefault("a", "b"); got != "a" {
		t.Fatalf("stringOrDefault(a,b) = %q, want a", got)
	}
	if got := stringOrDefault("", "b"); got != "b" {
		t.Fatalf("stringOrDefault(\"\",b) = %q, want b", got)
	}
}

func TestLabelsMatch(t *testing.T) {
	tests := []struct {
		name string
		have map[string]string
		want map[string]string
		ok   bool
	}{
		{"empty want", map[string]string{"a": "1"}, map[string]string{}, true},
		{"subset match", map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "1"}, true},
		{"value mismatch", map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
		{"missing key", map[string]string{"a": "1"}, map[string]string{"c": "3"}, false},
		{"full match", map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "1", "b": "2"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := labelsMatch(tt.have, tt.want); got != tt.ok {
				t.Fatalf("labelsMatch(%v,%v) = %v, want %v", tt.have, tt.want, got, tt.ok)
			}
		})
	}
}

func TestProxyHostPort(t *testing.T) {
	tests := []struct {
		in       string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{"teleport.example.com:443", "teleport.example.com", 443, false},
		{"teleport.example.com", "teleport.example.com", 443, false},
		{"teleport.example.com:3080", "teleport.example.com", 3080, false},
		{"[::1]:443", "::1", 443, false},
		{"teleport.example.com:notaport", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			host, port, err := proxyHostPort(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tt.wantHost || port != tt.wantPort {
				t.Fatalf("proxyHostPort(%q) = (%q,%d), want (%q,%d)", tt.in, host, port, tt.wantHost, tt.wantPort)
			}
		})
	}
}

func TestParseALPNUpgradeMode(t *testing.T) {
	tests := []struct {
		in      string
		want    ALPNConnUpgradeMode
		wantErr bool
	}{
		{"", ALPNAuto, false},
		{"auto", ALPNAuto, false},
		{"AUTO", ALPNAuto, false},
		{"yes", ALPNYes, false},
		{"true", ALPNYes, false},
		{"required", ALPNYes, false},
		{"no", ALPNNo, false},
		{"false", ALPNNo, false},
		{"never", ALPNNo, false},
		{" yes ", ALPNYes, false},
		{"maybe", ALPNAuto, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseALPNUpgradeMode(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseALPNUpgradeMode(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestTunnelUpgradeMode(t *testing.T) {
	tests := []struct {
		in   ALPNConnUpgradeMode
		want tunnel.ALPNUpgradeMode
	}{
		{ALPNAuto, tunnel.ALPNUpgradeAuto},
		{ALPNYes, tunnel.ALPNUpgradeYes},
		{ALPNNo, tunnel.ALPNUpgradeNo},
	}
	for _, tt := range tests {
		if got := tunnelUpgradeMode(tt.in); got != tt.want {
			t.Fatalf("tunnelUpgradeMode(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
