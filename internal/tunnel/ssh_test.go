package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/ssh"
)

func newAuthorizedKey(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return ssh.MarshalAuthorizedKey(pub)
}

func TestMakeHostKeyCallbackEmpty(t *testing.T) {
	if _, err := makeHostKeyCallback(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty CA set")
	}
}

func TestMakeHostKeyCallbackSkipsBadEntries(t *testing.T) {
	good := newAuthorizedKey(t)
	bad := []byte("not-a-valid-authorized-key-line\n")

	// A mix of bad and good entries should still yield a usable callback
	// because the good key is parsed and the bad one is skipped.
	cb, err := makeHostKeyCallback(context.Background(), [][]byte{bad, good})
	if err != nil {
		t.Fatalf("expected callback despite a bad entry, got %v", err)
	}
	if cb == nil {
		t.Fatal("expected non-nil callback")
	}
}

func TestMakeHostKeyCallbackAllBad(t *testing.T) {
	bad := []byte("garbage\n")
	if _, err := makeHostKeyCallback(context.Background(), [][]byte{bad}); err == nil {
		t.Fatal("expected error when all CA entries are invalid")
	}
}

func TestMakeHostKeyCallbackMultipleLines(t *testing.T) {
	a := newAuthorizedKey(t)
	b := newAuthorizedKey(t)
	combined := append(append([]byte{}, a...), b...)

	cb, err := makeHostKeyCallback(context.Background(), [][]byte{combined})
	if err != nil {
		t.Fatalf("expected callback, got %v", err)
	}
	if cb == nil {
		t.Fatal("expected non-nil callback")
	}
}

func TestNewSSHTunnelValidation(t *testing.T) {
	tests := []struct {
		name string
		opts SSHOptions
	}{
		{"no proxy", SSHOptions{GatewayNode: "g", TargetHost: "h", TargetPort: 1, SSHLogin: "u"}},
		{"no gateway", SSHOptions{ProxyAddress: "p:443", TargetHost: "h", TargetPort: 1, SSHLogin: "u"}},
		{"no target", SSHOptions{ProxyAddress: "p:443", GatewayNode: "g", SSHLogin: "u"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewSSHTunnel(t.Context(), tt.opts); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
