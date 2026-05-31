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
	"github.com/gravitational/teleport/api/constants"
	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/api/utils"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/auth/idtoken"
)

// alpnProxyGRPCInsecure routes an unauthenticated gRPC connection to the
// JoinService (before we hold credentials). Hardcoded rather than imported so
// this provider need not link Teleport's GPL/AGPL packages; the value lives
// upstream as ProtocolProxyGRPCInsecure in lib/srv/alpnproxy/common.
const alpnProxyGRPCInsecure = "teleport-proxy-grpc"

// joinResultTTL is the requested cert validity; the auth server may clamp it
// to the join token's max TTL.
const joinResultTTL = time.Hour

// credentialsFromJoin performs a delegated (OIDC-family) join and returns API
// credentials from the issued certs: the in-process equivalent of a one-shot
// tbot run, built only against the Apache-2.0 api/ module.
func (c Config) credentialsFromJoin(ctx context.Context) (credentialSet, error) {
	if !idtoken.IsSupported(c.JoinMethod) {
		return credentialSet{}, fmt.Errorf("join_method %q is not supported; supported methods: %s", c.JoinMethod, strings.Join(idtoken.Supported(), ", "))
	}

	audience := c.JoinAudience
	if audience == "" {
		audience = hostOnly(c.ProxyAddress)
	}

	idToken, err := idtoken.Fetch(ctx, c.JoinMethod, audience)
	if err != nil {
		return credentialSet{}, err
	}

	// The cluster signs the public half; the private half backs the creds.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return credentialSet{}, fmt.Errorf("generating private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return credentialSet{}, fmt.Errorf("marshaling public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return credentialSet{}, fmt.Errorf("marshaling private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	joinClient, closeConn, err := c.dialJoinService(ctx)
	if err != nil {
		return credentialSet{}, err
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
		return credentialSet{}, fmt.Errorf("joining cluster via %s join method: %w (check the join token name, that the token's allow rules match this workload, and that the audience claim is %q)", c.JoinMethod, err, audience)
	}

	clusterName, err := clusterNameFromCert(certs.TLS)
	if err != nil {
		return credentialSet{}, err
	}

	tlsConfig, err := tlsConfigFromCerts(certs, keyPEM)
	if err != nil {
		return credentialSet{}, err
	}
	applyProxyAuthRouting(tlsConfig, hostOnly(c.ProxyAddress), clusterName, certs.TLSCACerts, c.Insecure)

	return credentialSet{
		creds:  []tpclient.Credentials{tpclient.LoadTLS(tlsConfig)},
		dialer: c.proxyAuthDialer(ctx),
	}, nil
}

// proxyAuthDialer returns a dialer pinned to the proxy address for the
// post-join auth client. Pinning an explicit dialer makes the SDK route auth
// through the proxy and skip its auth-server fallback, which is unreachable on
// proxy-only topologies. It returns a raw connection (optionally after the
// ALPN HTTPS upgrade); the gRPC layer then performs the single TLS handshake
// using the auth-routing credentials.
func (c Config) proxyAuthDialer(ctx context.Context) tpclient.ContextDialer {
	base := tpclient.NewDialer(ctx, 30*time.Second, 20*time.Second,
		tpclient.WithInsecureSkipVerify(c.Insecure),
		tpclient.WithALPNConnUpgrade(c.resolveALPNUpgrade(ctx)),
	)
	return tpclient.ContextDialerFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
		return base.DialContext(ctx, "tcp", c.ProxyAddress)
	})
}

// applyProxyAuthRouting configures a join credential's TLS config to route auth
// through the proxy: the proxy host as SNI (verifying its public cert) plus the
// auth ALPN protocol carrying the cluster route, so the connection terminates
// at the proxy instead of dialing the auth server directly. Mirrors
// dialJoinService and tunnel/db.go.
func applyProxyAuthRouting(tlsConfig *tls.Config, proxyHost, clusterName string, caCerts [][]byte, insecure bool) {
	tlsConfig.ServerName = proxyHost
	tlsConfig.NextProtos = []string{constants.ALPNSNIAuthProtocol + utils.EncodeClusterName(clusterName)}
	if insecure {
		tlsConfig.InsecureSkipVerify = true
		return
	}
	// Trust the public proxy cert (system roots) alongside the cluster CAs the
	// proxy presents during ALPN connection upgrade.
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, ca := range caCerts {
		pool.AppendCertsFromPEM(ca)
	}
	tlsConfig.RootCAs = pool
}

// clusterNameFromCert reads the cluster name from a Teleport-issued TLS cert,
// where it is the issuing CA's Organization.
func clusterNameFromCert(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", errors.New("join service returned an unparseable TLS certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parsing issued certificate: %w", err)
	}
	if len(cert.Issuer.Organization) == 0 || cert.Issuer.Organization[0] == "" {
		return "", errors.New("issued certificate has no cluster name in its issuer")
	}
	return cert.Issuer.Organization[0], nil
}

// dialJoinService opens an unauthenticated gRPC connection to the proxy's
// JoinService over the ALPN-routed proxy port. Call the returned func to close.
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

	upgradeRequired := c.resolveALPNUpgrade(ctx)
	dialer := tpclient.NewALPNDialer(tpclient.ALPNDialerConfig{
		DialTimeout:             15 * time.Second,
		KeepAlivePeriod:         30 * time.Second,
		TLSConfig:               tlsConfig,
		ALPNConnUpgradeRequired: upgradeRequired,
	})

	conn, err := grpc.NewClient(
		c.ProxyAddress,
		// The ALPN dialer already encrypts the conn, so gRPC's own transport
		// creds are insecure.
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
