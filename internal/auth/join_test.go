package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
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

func TestResolveALPNUpgrade(t *testing.T) {
	// yes/no are honored without consulting the (network) probe.
	c := Config{}
	if got := c.resolveALPNUpgrade(context.Background(), ALPNUpgradeYes); !got {
		t.Fatal("ALPNUpgradeYes should resolve to true")
	}
	if got := c.resolveALPNUpgrade(context.Background(), ALPNUpgradeNo); got {
		t.Fatal("ALPNUpgradeNo should resolve to false")
	}
}

func TestSSHCertFromBytes(t *testing.T) {
	key, certBytes := issueTestSSHCert(t)

	cert, err := sshCertFromBytes(certBytes)
	if err != nil {
		t.Fatalf("sshCertFromBytes: %v", err)
	}
	if _, err := sshClientSigner(cert, key); err != nil {
		t.Fatalf("sshClientSigner: %v", err)
	}
	if _, err := sshCertFromBytes([]byte("not a key")); err == nil {
		t.Fatal("expected error for invalid ssh cert bytes")
	}
}

func TestHostKeyCallback(t *testing.T) {
	_, certBytes := issueTestSSHCert(t)
	if _, err := hostKeyCallback([][]byte{certBytes}); err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if _, err := hostKeyCallback(nil); err == nil {
		t.Fatal("expected error when no SSH CAs provided")
	}
}

// issueTestSSHCert returns an ECDSA private key and a marshaled SSH certificate
// (over that key) signed by a throwaway CA, for exercising the SSH helpers.
func issueTestSSHCert(t *testing.T) (crypto.Signer, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caSigner, err := ssh.NewSignerFromKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := &ssh.Certificate{
		Key:         pub,
		CertType:    ssh.UserCert,
		ValidBefore: ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatal(err)
	}
	return key, ssh.MarshalAuthorizedKey(cert)
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
