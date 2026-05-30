package tunnel

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	tpproxy "github.com/gravitational/teleport/api/client/proxy"
	"golang.org/x/crypto/ssh"
)

// SSHOptions configures an SSHTunnel.
type SSHOptions struct {
	// ProxyAddress is the Teleport proxy host:port the tunnel dials.
	ProxyAddress string
	// Cluster is the Teleport cluster to route through. Empty defaults to
	// the cluster the proxy belongs to.
	Cluster string
	// GatewayNode is the Teleport node hostname the SSH session is opened
	// against (the "jump host"). Required.
	GatewayNode string
	// TargetHost / TargetPort is the address to forward to from the
	// gateway node, equivalent to the right-hand side of `ssh -L
	// LOCAL:TARGET_HOST:TARGET_PORT GATEWAY`.
	TargetHost string
	TargetPort int
	// SSHLogin is the OS user to authenticate as on the gateway node.
	SSHLogin string

	// SSHCert / PrivateKey / SSHCAs come from sshcerts.Issue. The cert is
	// presented as the SSH client identity; the SSH CAs are used for the
	// HostKeyCallback so we trust Teleport-issued host keys.
	SSHCert    *ssh.Certificate
	PrivateKey *rsa.PrivateKey
	SSHCAs     [][]byte

	// TLSConfig is the mTLS config used to talk to the proxy's gRPC
	// transport (TLS routing). Typically obtained from
	// (*client.Client).Config().
	TLSConfig *tls.Config

	// ALPNUpgrade selects the ALPN connection upgrade behavior, mirroring
	// the DB tunnel's behavior. Required for L7-LB-fronted proxies.
	ALPNUpgrade ALPNUpgradeMode

	// ListenAddr is the local address to listen on, e.g. "127.0.0.1:0".
	ListenAddr string
}

// SSHTunnel is a goroutine-backed local TCP listener that forwards each
// accepted connection to a Teleport-managed gateway node via the proxy's
// SSH transport, then opens a direct-tcpip channel from there to a target
// host:port. This is the in-process equivalent of `tsh ssh -N -L
// LOCAL:TARGET GATEWAY`.
//
// NOTE: this implementation is build-verified but has not been smoke-tested
// against a live cluster yet. The DB tunnel followed the same shape and
// works end-to-end; the same primitives are reused here, but the proxy.SSH
// → ssh.NewClientConn → direct-tcpip path has more moving pieces (host key
// verification, login matching, etc.) that may need real-world tuning.
type SSHTunnel struct {
	listener    net.Listener
	proxyClient *tpproxy.Client
	sshClient   *ssh.Client
	target      string

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
}

// NewSSHTunnel issues nothing on its own (callers pass an already-issued
// SSH cert via opts) but does establish the SSH session to the gateway
// node and starts the local accept loop.
func NewSSHTunnel(parent context.Context, opts SSHOptions) (*SSHTunnel, error) {
	if opts.ProxyAddress == "" {
		return nil, errors.New("proxy_address is required")
	}
	if opts.GatewayNode == "" {
		return nil, errors.New("gateway_node is required")
	}
	if opts.TargetHost == "" || opts.TargetPort == 0 {
		return nil, errors.New("target_host and target_port are required")
	}
	if opts.SSHCert == nil || opts.PrivateKey == nil {
		return nil, errors.New("ssh cert and private key are required")
	}
	if opts.SSHLogin == "" {
		return nil, errors.New("ssh_login is required")
	}
	if opts.TLSConfig == nil {
		return nil, errors.New("TLSConfig is required (typically from (*client.Client).Config())")
	}

	// Build the SSH client config. Auth uses the Teleport-signed cert; the
	// host key callback trusts only keys signed by the cluster's SSH host
	// CAs (no TOFU, no static authorized_keys).
	signer, err := ssh.NewSignerFromKey(opts.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("building ssh signer: %w", err)
	}
	certSigner, err := ssh.NewCertSigner(opts.SSHCert, signer)
	if err != nil {
		return nil, fmt.Errorf("building ssh cert signer: %w", err)
	}
	hostKeyCallback, err := makeHostKeyCallback(opts.SSHCAs)
	if err != nil {
		return nil, fmt.Errorf("building host key callback: %w", err)
	}
	sshConfig := &ssh.ClientConfig{
		User:            opts.SSHLogin,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(certSigner)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}

	// Pick ALPN upgrade behavior. For tpproxy.NewClient there's no
	// auto-detect helper, so we do the same probe as the DB tunnel when
	// in auto mode.
	upgradeRequired := false
	switch opts.ALPNUpgrade {
	case ALPNUpgradeYes:
		upgradeRequired = true
	case ALPNUpgradeNo:
		upgradeRequired = false
	default:
		// Auto: leave as false. Callers who know their proxy is behind
		// an L7 LB should pass ALPNUpgradeYes explicitly (same caveat
		// as the DB tunnel; tpclient.IsALPNConnUpgradeRequired is
		// unreliable for many real-world LBs).
	}

	pc, err := tpproxy.NewClient(parent, tpproxy.ClientConfig{
		ProxyAddress:            opts.ProxyAddress,
		TLSRoutingEnabled:       true,
		TLSConfigFunc:           func(_ string) (*tls.Config, error) { return opts.TLSConfig.Clone(), nil },
		SSHConfig:               sshConfig,
		ALPNConnUpgradeRequired: upgradeRequired,
		DialTimeout:             20 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("opening proxy client: %w", err)
	}

	// DialHost asks the proxy to forward us to the SSH service on the
	// gateway node. The returned conn is a raw stream the proxy is
	// piping to the node's SSH listener; we layer SSH client protocol
	// on top of it ourselves.
	hostConn, _, err := pc.DialHost(parent, opts.GatewayNode+":0", opts.Cluster, nil)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("dialing gateway node %s: %w", opts.GatewayNode, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(hostConn, opts.GatewayNode, sshConfig)
	if err != nil {
		_ = hostConn.Close()
		_ = pc.Close()
		return nil, fmt.Errorf("ssh client handshake to %s: %w", opts.GatewayNode, err)
	}
	sshClient := ssh.NewClient(sshConn, chans, reqs)

	addr := opts.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = sshClient.Close()
		_ = pc.Close()
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(parent)
	t := &SSHTunnel{
		listener:    ln,
		proxyClient: pc,
		sshClient:   sshClient,
		target:      net.JoinHostPort(opts.TargetHost, strconv.Itoa(opts.TargetPort)),
		ctx:         ctx,
		cancel:      cancel,
	}
	log.Printf("teleportconnect ssh-tunnel: listening on %s -> %s via gateway %s (alpn_upgrade=%v)",
		ln.Addr(), t.target, opts.GatewayNode, upgradeRequired)
	go t.acceptLoop()
	return t, nil
}

// LocalHost / LocalPort mirror DBTunnel for symmetry.
func (t *SSHTunnel) LocalHost() string {
	if a, ok := t.listener.Addr().(*net.TCPAddr); ok {
		return a.IP.String()
	}
	return "127.0.0.1"
}

func (t *SSHTunnel) LocalPort() int {
	if a, ok := t.listener.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// Close stops accepting new connections and tears down the SSH session
// and proxy client. Safe to call multiple times.
func (t *SSHTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	t.cancel()
	var firstErr error
	if err := t.listener.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := t.sshClient.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := t.proxyClient.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (t *SSHTunnel) acceptLoop() {
	for {
		client, err := t.listener.Accept()
		if err != nil {
			return
		}
		go t.handleConn(client)
	}
}

func (t *SSHTunnel) handleConn(client net.Conn) {
	defer client.Close()
	upstream, err := t.sshClient.DialContext(t.ctx, "tcp", t.target)
	if err != nil {
		log.Printf("teleportconnect ssh-tunnel: dial %s failed: %v", t.target, err)
		return
	}
	defer upstream.Close()

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

// makeHostKeyCallback builds an ssh.HostKeyCallback that accepts host keys
// signed by any of the supplied SSH CAs (in authorized_keys format).
func makeHostKeyCallback(sshCAs [][]byte) (ssh.HostKeyCallback, error) {
	cas := make([]ssh.PublicKey, 0, len(sshCAs))
	for _, raw := range sshCAs {
		// Each entry may contain multiple authorized_keys lines.
		for len(raw) > 0 {
			pk, _, _, rest, err := ssh.ParseAuthorizedKey(raw)
			if err != nil {
				// Stop on the first parse failure for this entry; ignore
				// trailing whitespace etc.
				break
			}
			cas = append(cas, pk)
			raw = rest
		}
	}
	if len(cas) == 0 {
		return nil, errors.New("no SSH CAs provided; cannot verify host keys")
	}
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool {
			for _, ca := range cas {
				if keysEqual(auth, ca) {
					return true
				}
			}
			return false
		},
	}
	return checker.CheckHostKey, nil
}

func keysEqual(a, b ssh.PublicKey) bool {
	return string(a.Marshal()) == string(b.Marshal())
}
