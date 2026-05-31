//go:build reprojoin

package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	tpclient "github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/types"
	"golang.org/x/crypto/ssh"
)

// TestReproJoin performs a real bot join against the local integration cluster
// using the static `token` join method, then builds the post-join client via
// the same proxyClientConfig path Build uses and calls Ping. Run with:
//
//	go test -tags reprojoin -run TestReproJoin -v ./internal/auth/
func TestReproJoin(t *testing.T) {
	const (
		proxy   = "localhost:3080"
		token   = "bot-join-token"
		cluster = "teleportconnect.local"
	)
	c := Config{ProxyAddress: proxy, JoinToken: token, Insecure: true, JoinALPNUpgrade: ALPNUpgradeNo, AuthALPNUpgrade: ALPNUpgradeNo}
	ctx := context.Background()

	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pubDER, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	keyDER, _ := x509.MarshalPKCS8PrivateKey(priv)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	sshSigner, _ := ssh.NewSignerFromKey(priv)
	sshPub := ssh.MarshalAuthorizedKey(sshSigner.PublicKey())

	joinClient, closeConn, err := c.dialJoinService(ctx)
	if err != nil {
		t.Fatalf("dialJoinService: %v", err)
	}
	defer closeConn()

	certs, err := joinClient.RegisterUsingToken(ctx, &types.RegisterUsingTokenRequest{
		Token:        token,
		Role:         types.RoleBot,
		PublicTLSKey: pubPEM,
		PublicSSHKey: sshPub,
		Expires:      ptrTime(time.Now().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("RegisterUsingToken: %v", err)
	}
	t.Logf("join OK; TLS cert len=%d CAs=%d SSH len=%d", len(certs.TLS), len(certs.TLSCACerts), len(certs.SSH))

	cn, err := clusterNameFromCert(certs.TLS)
	if err != nil {
		t.Fatalf("clusterNameFromCert: %v", err)
	}
	t.Logf("derived cluster name = %q (expect %q)", cn, cluster)

	cfg, err := c.proxyClientConfig(ctx, certs, keyPEM, priv, cn)
	if err != nil {
		t.Fatalf("proxyClientConfig: %v", err)
	}

	clt, err := tpclient.New(ctx, *cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	defer clt.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pong, err := clt.Ping(pingCtx)
	if err != nil {
		t.Fatalf("PING FAILED: %v", err)
	}
	t.Logf("PING OK: cluster=%s version=%s", pong.ClusterName, pong.ServerVersion)
}
