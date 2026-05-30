package auth

import (
	"context"
	"testing"
)

func TestHostOnly(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"teleport.example.com:443", "teleport.example.com"},
		{"teleport.example.com", "teleport.example.com"},
		{"[::1]:443", "::1"},
		{"127.0.0.1:3080", "127.0.0.1"},
	}
	for _, tt := range tests {
		if got := hostOnly(tt.in); got != tt.want {
			t.Fatalf("hostOnly(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTLSConfigFromCertsValidation(t *testing.T) {
	if _, err := tlsConfigFromCerts(nil, nil); err == nil {
		t.Fatal("expected error for nil certs")
	}
}

func TestCredentialsFromJoinUnsupportedMethod(t *testing.T) {
	c := Config{ProxyAddress: "p:443", JoinMethod: "iam", JoinToken: "ci"}
	if _, err := c.credentialsFromJoin(context.Background()); err == nil {
		t.Fatal("expected error for unsupported join method")
	}
}
