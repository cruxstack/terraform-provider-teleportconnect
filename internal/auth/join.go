package auth

import (
	"context"
	"crypto"
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
	tpproxy "github.com/gravitational/teleport/api/client/proxy"
	"github.com/gravitational/teleport/api/client/webclient"
	"github.com/gravitational/teleport/api/types"
	"golang.org/x/crypto/ssh"
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

	// Fresh ECDSA P-256 keypair; the cluster signs the public half (as both
	// TLS and SSH), the private half backs the issued certs.
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

	sshSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return credentialSet{}, fmt.Errorf("building ssh signer: %w", err)
	}
	sshPub := ssh.MarshalAuthorizedKey(sshSigner.PublicKey())

	joinClient, closeConn, err := c.dialJoinService(ctx)
	if err != nil {
		return credentialSet{}, err
	}
	defer closeConn()

	certs, err := joinClient.RegisterUsingToken(ctx, &types.RegisterUsingTokenRequest{
		Token:        c.JoinToken,
		Role:         types.RoleBot,
		PublicTLSKey: pubPEM,
		PublicSSHKey: sshPub,
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

	cfg, err := c.proxyClientConfig(ctx, certs, keyPEM, priv, clusterName)
	if err != nil {
		return credentialSet{}, err
	}
	return credentialSet{clientConfig: cfg}, nil
}

// proxyClientConfig builds the post-join auth client config the same way
// tsh/tbot do: it constructs an api/client/proxy.Client against the proxy
// (discovering TLS routing and connection-upgrade requirements) and returns
// its ClientConfig, so auth is reached through the proxy rather than dialed
// directly.
func (c Config) proxyClientConfig(ctx context.Context, certs *proto.Certs, keyPEM []byte, signer crypto.Signer, clusterName string) (*tpclient.Config, error) {
	tlsConfig, err := tlsConfigFromCerts(certs, keyPEM)
	if err != nil {
		return nil, err
	}
	if c.Insecure {
		tlsConfig.InsecureSkipVerify = true
	}

	sshCert, err := sshCertFromBytes(certs.SSH)
	if err != nil {
		return nil, err
	}
	certSigner, err := sshClientSigner(sshCert, signer)
	if err != nil {
		return nil, err
	}
	hostKeys, err := hostKeyCallback(certs.SSHCACerts)
	if err != nil {
		return nil, err
	}
	sshConfig := &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(certSigner)},
		HostKeyCallback: hostKeys,
		Timeout:         20 * time.Second,
	}

	tlsRouting, err := c.proxyTLSRoutingEnabled(ctx)
	if err != nil {
		return nil, err
	}

	pc, err := tpproxy.NewClient(ctx, tpproxy.ClientConfig{
		ProxyAddress:            c.ProxyAddress,
		TLSRoutingEnabled:       tlsRouting,
		TLSConfigFunc:           func(string) (*tls.Config, error) { return tlsConfig.Clone(), nil },
		SSHConfig:               sshConfig,
		ALPNConnUpgradeRequired: c.resolveALPNUpgrade(ctx, c.AuthALPNUpgrade),
		InsecureSkipVerify:      c.Insecure,
		DialTimeout:             20 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("creating proxy client: %w", err)
	}

	cfg, err := pc.ClientConfig(ctx, clusterName)
	if err != nil {
		return nil, fmt.Errorf("building proxy auth client config: %w", err)
	}
	cfg.Context = ctx
	cfg.InsecureAddressDiscovery = c.Insecure
	return &cfg, nil
}

// proxyTLSRoutingEnabled queries the proxy's /webapi/find (same call tsh makes)
// to learn whether the cluster uses TLS routing.
func (c Config) proxyTLSRoutingEnabled(ctx context.Context) (bool, error) {
	resp, err := webclient.Find(&webclient.Config{
		Context:   ctx,
		ProxyAddr: c.ProxyAddress,
		Insecure:  c.Insecure,
	})
	if err != nil {
		return false, fmt.Errorf("querying proxy settings at %s: %w", c.ProxyAddress, err)
	}
	return resp.Proxy.TLSRoutingEnabled, nil
}

// sshCertFromBytes parses an SSH certificate in authorized_keys format.
func sshCertFromBytes(sshCert []byte) (*ssh.Certificate, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey(sshCert)
	if err != nil {
		return nil, fmt.Errorf("parsing issued ssh certificate: %w", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("join service returned a non-certificate ssh key")
	}
	return cert, nil
}

// sshClientSigner combines the issued SSH certificate with the local private
// key into a signer usable for SSH client auth.
func sshClientSigner(cert *ssh.Certificate, key crypto.Signer) (ssh.Signer, error) {
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("building ssh signer: %w", err)
	}
	certSigner, err := ssh.NewCertSigner(cert, signer)
	if err != nil {
		return nil, fmt.Errorf("building ssh cert signer: %w", err)
	}
	return certSigner, nil
}

// hostKeyCallback trusts SSH host keys signed by any of the supplied SSH CAs
// (authorized_keys format). Unparseable entries are skipped.
func hostKeyCallback(sshCAs [][]byte) (ssh.HostKeyCallback, error) {
	cas := make([]ssh.PublicKey, 0, len(sshCAs))
	for _, raw := range sshCAs {
		for len(raw) > 0 {
			pk, _, _, rest, err := ssh.ParseAuthorizedKey(raw)
			if err != nil {
				break
			}
			cas = append(cas, pk)
			raw = rest
		}
	}
	if len(cas) == 0 {
		return nil, errors.New("join service returned no SSH CAs")
	}
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool {
			for _, ca := range cas {
				if string(auth.Marshal()) == string(ca.Marshal()) {
					return true
				}
			}
			return false
		},
	}
	return checker.CheckHostKey, nil
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

	upgradeRequired := c.resolveALPNUpgrade(ctx, c.JoinALPNUpgrade)
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
