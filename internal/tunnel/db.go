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
	"net"
	"strings"
	"sync"
	"time"

	tpclient "github.com/gravitational/teleport/api/client"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// ALPN wire protocol values for Teleport database access. Hardcoded (rather
// than imported) so this provider need not link Teleport's GPL/AGPL packages;
// the values live upstream in lib/srv/alpnproxy/common/protocols.go.
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

// ALPNForProtocol maps a Teleport database protocol (e.g. "postgres") to the
// ALPN wire value used in the TLS handshake with the proxy.
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
	ALPNUpgradeAuto ALPNUpgradeMode = iota // probe via IsALPNConnUpgradeRequired
	ALPNUpgradeYes                         // force upgrade (L7 LB fronting the proxy)
	ALPNUpgradeNo                          // direct TLS routing
)

// DBOptions configures a DBTunnel.
type DBOptions struct {
	ProxyAddress string
	Protocol     string
	// ClientCertPEM carries the RouteToDatabase routing claim; with
	// ClientKeyPEM and CAPEM it is the Teleport-issued database client
	// keypair plus the cluster TLS CA bundle.
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	CAPEM         []byte
	ListenAddr    string // empty defaults to 127.0.0.1:0
	ALPNUpgrade   ALPNUpgradeMode
}

// DBTunnel is a goroutine-backed local TCP listener that forwards each
// accepted connection to a Teleport proxy via TLS routing.
//
// Lifecycle: NewDBTunnel starts accepting immediately. Close stops the
// listener, cancels the context, and force-closes all in-flight
// connections so nothing leaks past terraform apply.
type DBTunnel struct {
	listener   net.Listener
	alpnDialer tpclient.ContextDialer
	proxyAddr  string

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
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
		conns:      make(map[net.Conn]struct{}),
	}
	tflog.Debug(ctx, "database tunnel listening", map[string]any{
		"local_addr":   ln.Addr().String(),
		"proxy_addr":   opts.ProxyAddress,
		"alpn":         alpn,
		"alpn_upgrade": upgradeRequired,
	})
	t.wg.Add(1)
	go t.acceptLoop()
	return t, nil
}

// LocalHost returns the address the tunnel is listening on.
func (t *DBTunnel) LocalHost() string {
	if a, ok := t.listener.Addr().(*net.TCPAddr); ok {
		return a.IP.String()
	}
	return "127.0.0.1"
}

// LocalPort returns the OS-assigned local port.
func (t *DBTunnel) LocalPort() int {
	if a, ok := t.listener.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// Close stops accepting new connections, cancels the background context,
// and force-closes any in-flight connections. Safe to call multiple times.
func (t *DBTunnel) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.cancel()
	err := t.listener.Close()
	for c := range t.conns {
		_ = c.Close()
	}
	t.mu.Unlock()

	t.wg.Wait()
	return err
}

// trackConn records an active client connection. Returns false if the
// tunnel is already closed, in which case the caller should not proceed.
func (t *DBTunnel) trackConn(c net.Conn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.conns[c] = struct{}{}
	return true
}

func (t *DBTunnel) untrackConn(c net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, c)
}

func (t *DBTunnel) acceptLoop() {
	defer t.wg.Done()
	for {
		client, err := t.listener.Accept()
		if err != nil {
			return // listener closed or unrecoverable
		}
		if !t.trackConn(client) {
			_ = client.Close()
			return
		}
		t.wg.Add(1)
		go t.handleConn(client)
	}
}

func (t *DBTunnel) handleConn(client net.Conn) {
	defer t.wg.Done()
	defer t.untrackConn(client)
	defer func() { _ = client.Close() }()

	upstream, err := t.alpnDialer.DialContext(t.ctx, "tcp", t.proxyAddr)
	if err != nil {
		tflog.Error(t.ctx, "database tunnel upstream dial failed", map[string]any{
			"proxy_addr": t.proxyAddr,
			"error":      err.Error(),
		})
		return
	}
	defer func() { _ = upstream.Close() }()
	if tlsConn, ok := upstream.(*tls.Conn); ok {
		tflog.Trace(t.ctx, "database tunnel upstream connected", map[string]any{
			"negotiated_alpn": tlsConn.ConnectionState().NegotiatedProtocol,
		})
	}

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
