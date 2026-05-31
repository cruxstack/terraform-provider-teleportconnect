// Package auth builds an authenticated Teleport API client from provider
// configuration. Each supported auth method maps to a different Credentials
// implementation in the upstream Teleport SDK.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/gravitational/teleport/api/client"
)

// ALPNUpgradeMode selects the ALPN HTTPS connection-upgrade behavior for the
// join/auth dials, mirroring the provider's alpn_conn_upgrade setting.
type ALPNUpgradeMode int

const (
	ALPNUpgradeAuto ALPNUpgradeMode = iota // probe via IsALPNConnUpgradeRequired
	ALPNUpgradeYes                         // force upgrade (L7 LB fronting the proxy)
	ALPNUpgradeNo                          // direct TLS routing
)

// Config is the resolved authentication configuration. Validate enforces that
// exactly one auth mode is set.
type Config struct {
	ProxyAddress string
	Cluster      string // leaf-cluster override; empty means the proxy's own cluster

	IdentityFilePath string
	IdentityFileData string
	UseLocalProfile  bool

	// JoinMethod/JoinToken select a delegated (OIDC-family) join: fetch the
	// platform's identity token and join in-process, no identity file or
	// tbot. One of: github, gitlab, kubernetes, spacelift.
	JoinMethod   string
	JoinToken    string
	JoinAudience string // expected aud claim; empty defaults to the proxy host

	ALPNUpgrade ALPNUpgradeMode // applies to the join/auth dials
	Insecure    bool            // disables proxy TLS verification; dev clusters only
}

// resolveALPNUpgrade decides whether the join/auth dials perform the HTTPS
// connection upgrade: honor an explicit yes/no, else fall back to the probe
// (which is unreliable behind some L7 LBs, hence the override).
func (c Config) resolveALPNUpgrade(ctx context.Context) bool {
	switch c.ALPNUpgrade {
	case ALPNUpgradeYes:
		return true
	case ALPNUpgradeNo:
		return false
	default:
		return client.IsALPNConnUpgradeRequired(ctx, c.ProxyAddress, c.Insecure)
	}
}

// Validate ensures the configuration has the minimum required fields and
// that exactly one authentication method is selected.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ProxyAddress) == "" {
		return errors.New("proxy_address is required")
	}

	var modes []string
	if c.IdentityFilePath != "" {
		modes = append(modes, "identity_file_path")
	}
	if c.IdentityFileData != "" {
		modes = append(modes, "identity_file_data")
	}
	if c.UseLocalProfile {
		modes = append(modes, "use_local_profile")
	}
	if c.JoinMethod != "" || c.JoinToken != "" {
		modes = append(modes, "join_method+join_token")
	}

	switch len(modes) {
	case 0:
		return errors.New("one of identity_file_path, identity_file_data, use_local_profile, or join_method+join_token must be set")
	case 1:
	default:
		return fmt.Errorf("multiple auth methods set: %s; pick exactly one", strings.Join(modes, ", "))
	}

	if (c.JoinMethod != "") != (c.JoinToken != "") {
		return errors.New("join_method and join_token must both be set")
	}

	return nil
}

// Build constructs a connected *client.Client for the configured auth mode.
// The caller owns the returned client and must Close() it.
func Build(ctx context.Context, c Config) (*client.Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	creds, err := c.credentials(ctx)
	if err != nil {
		return nil, err
	}

	cfg := client.Config{
		Addrs:                    []string{c.ProxyAddress},
		Credentials:              creds.creds,
		InsecureAddressDiscovery: c.Insecure,
		Context:                  ctx,
	}

	// The join path pins an explicit ALPN dialer to the proxy so auth routes
	// through it (verifying the proxy's public cert via the credentials' TLS
	// config) and the SDK never falls back to dialing the auth server
	// directly — which is unreachable on locked-down, proxy-only topologies.
	if creds.dialer != nil {
		cfg.Dialer = creds.dialer
	}

	clt, err := client.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to teleport proxy %s: %w", c.ProxyAddress, err)
	}
	return clt, nil
}

// credentialSet is the SDK credentials plus, for the join path, an explicit
// dialer pinned to the proxy (so there is no auth-server fallback).
type credentialSet struct {
	creds  []client.Credentials
	dialer client.ContextDialer
}

// credentials maps a Config to the Teleport SDK's credentials.
func (c Config) credentials(ctx context.Context) (credentialSet, error) {
	switch {
	case c.UseLocalProfile:
		// Empty dir => SDK default ~/.tsh, matched by the proxy host.
		return credentialSet{creds: []client.Credentials{client.LoadProfile("", profileNameFromProxy(c.ProxyAddress))}}, nil

	case c.IdentityFilePath != "":
		return credentialSet{creds: []client.Credentials{client.LoadIdentityFile(c.IdentityFilePath)}}, nil

	case c.IdentityFileData != "":
		return credentialSet{creds: []client.Credentials{client.LoadIdentityFileFromString(c.IdentityFileData)}}, nil

	case c.JoinMethod != "":
		return c.credentialsFromJoin(ctx)
	}
	return credentialSet{}, errors.New("no credentials configured")
}

// profileNameFromProxy returns the host portion of addr, which is how ~/.tsh
// profiles are named (e.g. teleport.example.com.yaml).
func profileNameFromProxy(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
