// Package tunnel implements local TCP listeners that proxy connections to
// Teleport-protected resources via the proxy's TLS routing (ALPN). This is
// the in-process equivalent of `tsh proxy db --tunnel`.
package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	tpclient "github.com/gravitational/teleport/api/client"
)

// ALPN wire protocol values for Teleport database access. Values mirror
// what `tsh proxy db --tunnel` and the Teleport proxy expect; they're
// part of the public protocol contract even though they live in the GPL
// portion of the Teleport tree (lib/srv/alpnproxy/common/protocols.go).
const (
	alpnPostgres      = "teleport-postgres"
	alpnMySQL         = "teleport-mysql"
	alpnMongoDB       = "teleport-mongodb"
	alpnRedis         = "teleport-redis-db"
	alpnSQLServer     = "teleport-sqlserver"
	alpnSnowflake     = "teleport-snowflake"
	alpnCassandra     = "teleport-cassandra"
	alpnElasticsearch = "teleport-elasticsearch"
	alpnOracle        = "teleport-oracle"
	alpnSpanner       = "teleport-spanner"
)

// ALPNForProtocol maps a Teleport database protocol (matches the Protocol
// field on a database resource, e.g. "postgres") to the ALPN wire value
// used during the TLS handshake with the Teleport proxy.
func ALPNForProtocol(protocol string) (string, error) {
	switch strings.ToLower(protocol) {
	case "postgres", "postgresql":
		return alpnPostgres, nil
	case "mysql", "mariadb":
		return alpnMySQL, nil
	case "mongodb":
		return alpnMongoDB, nil
	case "redis":
		return alpnRedis, nil
	case "sqlserver":
		return alpnSQLServer, nil
	case "snowflake":
		return alpnSnowflake, nil
	case "cassandra":
		return alpnCassandra, nil
	case "elasticsearch":
		return alpnElasticsearch, nil
	case "oracle":
		return alpnOracle, nil
	case "spanner":
		return alpnSpanner, nil
	default:
		return "", fmt.Errorf("unsupported database protocol %q for ALPN tunneling", protocol)
	}
}

// ALPNUpgradeMode controls whether the tunnel performs an HTTPS connection
// upgrade before TLS routing.
type ALPNUpgradeMode int

const (
	// ALPNUpgradeAuto probes the proxy via tpclient.IsALPNConnUpgradeRequired.
	ALPNUpgradeAuto ALPNUpgradeMode = iota
	// ALPNUpgradeYes forces the upgrade dance (needed when the proxy is
	// behind an L7 LB that terminates TLS with its own cert).
	ALPNUpgradeYes
	// ALPNUpgradeNo disables the upgrade and uses direct TLS routing.
	ALPNUpgradeNo
)

// DBOptions configures a DBTunnel.
type DBOptions struct {
	// ProxyAddress is the Teleport proxy host:port the tunnel dials.
	ProxyAddress string
	// Protocol is the Teleport database protocol (e.g. "postgres").
	Protocol string
	// ClientCertPEM, ClientKeyPEM and CAPEM are the Teleport-issued
	// database client cert (with RouteToDatabase baked in), its
	// matching private key, and the cluster TLS CA bundle.
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	CAPEM         []byte
	// ListenAddr is the local address to listen on, e.g. "127.0.0.1:0"
	// for an OS-assigned port.
	ListenAddr string
	// ALPNUpgrade selects the ALPN connection upgrade behavior.
	ALPNUpgrade ALPNUpgradeMode
}

// DBTunnel is a goroutine-backed local TCP listener that forwards each
// accepted connection to a Teleport proxy via TLS routing.
//
// Lifecycle: NewDBTunnel starts accepting immediately. Close stops the
// listener and waits briefly for in-flight handlers; tunnels still in the
// middle of a connection get cancelled when the provider process exits.
type DBTunnel struct {
	listener   net.Listener
	alpnDialer tpclient.ContextDialer
	proxyAddr  string

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
}

// NewDBTunnel builds the TLS config from the supplied PEM material, opens
// a local listener, and starts the accept loop.
func NewDBTunnel(parent context.Context, opts DBOptions) (*DBTunnel, error) {
	if opts.ProxyAddress == "" {
		return nil, errors.New("proxy_address is required")
	}
	if opts.Protocol == "" {
		return nil, errors.New("protocol is required")
	}
	if len(opts.ClientCertPEM) == 0 || len(opts.ClientKeyPEM) == 0 {
		return nil, errors.New("client cert and key are required")
	}

	alpn, err := ALPNForProtocol(opts.Protocol)
	if err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(opts.ClientCertPEM, opts.ClientKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading client keypair: %w", err)
	}

	caPool := x509.NewCertPool()
	if len(opts.CAPEM) > 0 {
		if !caPool.AppendCertsFromPEM(opts.CAPEM) {
			return nil, errors.New("CA bundle did not contain any valid PEM certificates")
		}
	}

	host, _, err := net.SplitHostPort(opts.ProxyAddress)
	if err != nil {
		return nil, fmt.Errorf("parsing proxy_address %q: %w", opts.ProxyAddress, err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   host,
		NextProtos:   []string{alpn},
		MinVersion:   tls.VersionTLS12,
	}

	// Decide whether to do the HTTPS connection upgrade dance. Some L7
	// LBs (AWS ALB, etc.) negotiate ALPN values back to the client but
	// still terminate TLS with their own cert, fooling the auto-probe.
	// Callers can override the auto-decision via DBOptions.ALPNUpgrade.
	upgradeRequired := false
	switch opts.ALPNUpgrade {
	case ALPNUpgradeYes:
		upgradeRequired = true
	case ALPNUpgradeNo:
		upgradeRequired = false
	default: // ALPNUpgradeAuto
		upgradeRequired = tpclient.IsALPNConnUpgradeRequired(parent, opts.ProxyAddress, false)
	}
	alpnDialer := tpclient.NewALPNDialer(tpclient.ALPNDialerConfig{
		DialTimeout:             15 * time.Second,
		KeepAlivePeriod:         30 * time.Second,
		TLSConfig:               tlsConfig,
		ALPNConnUpgradeRequired: upgradeRequired,
	})

	addr := opts.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(parent)
	t := &DBTunnel{
		listener:   ln,
		alpnDialer: alpnDialer,
		proxyAddr:  opts.ProxyAddress,
		ctx:        ctx,
		cancel:     cancel,
	}
	log.Printf("teleportconnect tunnel: listening on %s -> %s (alpn=%s, alpn_upgrade=%v)",
		ln.Addr(), opts.ProxyAddress, alpn, upgradeRequired)
	go t.acceptLoop()
	return t, nil
}

// LocalHost returns the address the tunnel is listening on (typically
// "127.0.0.1").
func (t *DBTunnel) LocalHost() string {
	if a, ok := t.listener.Addr().(*net.TCPAddr); ok {
		return a.IP.String()
	}
	return "127.0.0.1"
}

// LocalPort returns the OS-assigned local port the tunnel is listening on.
func (t *DBTunnel) LocalPort() int {
	if a, ok := t.listener.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// Close stops accepting new connections and cancels the background context.
// Safe to call multiple times.
func (t *DBTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	t.cancel()
	return t.listener.Close()
}

func (t *DBTunnel) acceptLoop() {
	for {
		client, err := t.listener.Accept()
		if err != nil {
			// Listener was closed (Close() called) or unrecoverable error.
			// Either way we exit; new connections won't be served.
			return
		}
		go t.handleConn(client)
	}
}

func (t *DBTunnel) handleConn(client net.Conn) {
	defer client.Close()

	// Per-connection upstream dial through the ALPN dialer. The dialer
	// internally handles HTTPS upgrade for L7-LB-fronted proxies and
	// performs the TLS-routing handshake with our client cert.
	upstream, err := t.alpnDialer.DialContext(t.ctx, "tcp", t.proxyAddr)
	if err != nil {
		// Provider stderr is plumbed to Terraform's debug logs.
		log.Printf("teleportconnect tunnel: upstream dial to %s failed: %v", t.proxyAddr, err)
		return
	}
	defer upstream.Close()
	if tlsConn, ok := upstream.(*tls.Conn); ok {
		log.Printf("teleportconnect tunnel: upstream connected, alpn=%q", tlsConn.ConnectionState().NegotiatedProtocol)
	}

	// Pipe in both directions concurrently. When either side closes,
	// propagate close to the other to avoid orphaned half-open sockets.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = closeWrite(upstream)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		_ = closeWrite(client)
		done <- struct{}{}
	}()
	<-done
}

// closeWrite issues a half-close where supported; falls back to full close.
func closeWrite(c net.Conn) error {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}
