package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
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

func TestClusterNameFromCert(t *testing.T) {
	const cluster = "teleport.cluster.local"
	leaf := issueTestCert(t, cluster)

	got, err := clusterNameFromCert(leaf)
	if err != nil {
		t.Fatalf("clusterNameFromCert: %v", err)
	}
	if got != cluster {
		t.Fatalf("got %q, want %q", got, cluster)
	}
}

func TestClusterNameFromCertErrors(t *testing.T) {
	if _, err := clusterNameFromCert([]byte("not a pem")); err == nil {
		t.Fatal("expected error for non-PEM input")
	}
	if _, err := clusterNameFromCert(issueTestCert(t, "")); err == nil {
		t.Fatal("expected error for cert without cluster name")
	}
}

// issueTestCert mints a leaf cert signed by a CA whose Organization is
// clusterName, mirroring how Teleport encodes the cluster in issued certs.
func issueTestCert(t *testing.T, clusterName string) []byte {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{clusterName}},
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "bot-ci"},
		NotAfter:     time.Now().Add(time.Hour),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
}
