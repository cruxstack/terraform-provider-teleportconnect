package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	tpclient "github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/auth/idtoken"
)

// alpnProxyGRPCInsecure is the ALPN protocol value the Teleport proxy uses to
// route an unauthenticated gRPC connection (used here to reach the
// JoinService before we hold any credentials). The value is part of the
// proxy's public protocol contract; it is defined in the GPL-licensed
// lib/srv/alpnproxy/common/protocols.go (ProtocolProxyGRPCInsecure) in the
// upstream Teleport tree, so it is reproduced here as a constant rather than
// imported, to keep this provider clear of the GPL/AGPL portions of Teleport.
// If upstream renames it, delegated joins break until this is updated.
const alpnProxyGRPCInsecure = "teleport-proxy-grpc"

// joinResultTTL is the requested validity of the certificates issued by the
// JoinService when joining as a bot. The auth server may clamp this to the
// join token's configured max TTL.
const joinResultTTL = time.Hour

// credentialsFromJoin performs a delegated (OIDC-family) join against the
// Teleport proxy's JoinService and returns API credentials built from the
// issued certificates. It is the in-process equivalent of a one-shot tbot
// run, implemented entirely against the Apache-2.0 api/ module.
func (c Config) credentialsFromJoin(ctx context.Context) ([]tpclient.Credentials, error) {
	if !idtoken.IsSupported(c.JoinMethod) {
		return nil, fmt.Errorf("join_method %q is not supported; supported methods: %s", c.JoinMethod, strings.Join(idtoken.Supported(), ", "))
	}

	audience := c.JoinAudience
	if audience == "" {
		audience = hostOnly(c.ProxyAddress)
	}

	idToken, err := idtoken.Fetch(ctx, c.JoinMethod, audience)
	if err != nil {
		return nil, err
	}

	// Fresh ECDSA P-256 keypair; the public half is signed by the cluster,
	// the private half stays in memory and backs the returned credentials.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshaling public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	joinClient, closeConn, err := c.dialJoinService(ctx)
	if err != nil {
		return nil, err
	}
	defer closeConn()

	certs, err := joinClient.RegisterUsingToken(ctx, &types.RegisterUsingTokenRequest{
		Token:        c.JoinToken,
		Role:         types.RoleBot,
		PublicTLSKey: pubPEM,
		IDToken:      idToken,
		Expires:      ptrTime(time.Now().Add(joinResultTTL)),
	})
	if err != nil {
		return nil, fmt.Errorf("joining cluster via %s join method: %w (check the join token name, that the token's allow rules match this workload, and that the audience claim is %q)", c.JoinMethod, err, audience)
	}

	tlsConfig, err := tlsConfigFromCerts(certs, keyPEM)
	if err != nil {
		return nil, err
	}
	return []tpclient.Credentials{tpclient.LoadTLS(tlsConfig)}, nil
}

// dialJoinService opens an unauthenticated gRPC connection to the proxy's
// JoinService over the ALPN-routed proxy port. The returned close function
// must be called to release the connection.
func (c Config) dialJoinService(ctx context.Context) (*tpclient.JoinServiceClient, func(), error) {
	host := hostOnly(c.ProxyAddress)

	tlsConfig := &tls.Config{
		ServerName: host,
		NextProtos: []string{alpnProxyGRPCInsecure},
		MinVersion: tls.VersionTLS12,
	}
	if c.Insecure {
		tlsConfig.InsecureSkipVerify = true
	} else {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		tlsConfig.RootCAs = pool
	}

	// The proxy may sit behind an L7 LB that requires the HTTPS connection
	// upgrade before ALPN routing works. Probe once (cheap) unless insecure.
	upgradeRequired := tpclient.IsALPNConnUpgradeRequired(ctx, c.ProxyAddress, c.Insecure)
	dialer := tpclient.NewALPNDialer(tpclient.ALPNDialerConfig{
		DialTimeout:             15 * time.Second,
		KeepAlivePeriod:         30 * time.Second,
		TLSConfig:               tlsConfig,
		ALPNConnUpgradeRequired: upgradeRequired,
	})

	conn, err := grpc.NewClient(
		c.ProxyAddress,
		// TLS is handled by the ALPN dialer; the gRPC transport itself runs
		// over the already-encrypted conn, so transport creds are insecure.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", addr)
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to proxy join service at %s: %w", c.ProxyAddress, err)
	}

	client := tpclient.NewJoinServiceClient(proto.NewJoinServiceClient(conn))
	return client, func() { _ = conn.Close() }, nil
}

// tlsConfigFromCerts builds a *tls.Config from the certificates returned by
// the JoinService and the locally-held private key.
func tlsConfigFromCerts(certs *proto.Certs, keyPEM []byte) (*tls.Config, error) {
	if certs == nil || len(certs.TLS) == 0 {
		return nil, errors.New("join service returned no TLS certificate")
	}
	cert, err := tls.X509KeyPair(certs.TLS, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading issued keypair: %w", err)
	}
	pool := x509.NewCertPool()
	for _, ca := range certs.TLSCACerts {
		if !pool.AppendCertsFromPEM(ca) {
			return nil, errors.New("join service returned an invalid CA certificate")
		}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func ptrTime(t time.Time) *time.Time { return &t }

// hostOnly returns the host portion of a host:port address, or the input
// unchanged when it has no port.
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}
