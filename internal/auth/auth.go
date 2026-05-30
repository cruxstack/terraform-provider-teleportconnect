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

// Config is the resolved provider configuration relevant to authentication.
// Exactly one of the auth-mode fields must be populated; Validate() enforces
// that.
type Config struct {
	// ProxyAddress is the Teleport proxy host:port. Required for every
	// auth mode.
	ProxyAddress string

	// Cluster is the optional leaf-cluster name for trusted-cluster
	// routing. Empty means "the cluster the proxy belongs to".
	Cluster string

	// Auth-mode fields. Validate() requires exactly one to be set.
	IdentityFilePath string
	IdentityFileData string
	UseLocalProfile  bool

	// JoinMethod / JoinToken select a delegated (OIDC-family) Machine ID
	// join: the provider fetches the platform's identity token and joins
	// the cluster in-process, with no identity file or tbot sidecar.
	// Supported methods: github, gitlab, kubernetes, spacelift.
	JoinMethod string
	JoinToken  string
	// JoinAudience overrides the expected audience claim of the identity
	// token. Empty defaults to the proxy host.
	JoinAudience string

	// Insecure disables proxy TLS verification (maps to the Teleport
	// SDK's InsecureAddressDiscovery, equivalent to `tsh --insecure`).
	// Should never be true outside of local development against a
	// self-signed cluster.
	Insecure bool
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
		// ok
	default:
		return fmt.Errorf("multiple auth methods set: %s; pick exactly one", strings.Join(modes, ", "))
	}

	// join_method and join_token must be set together.
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
		Credentials:              creds,
		InsecureAddressDiscovery: c.Insecure,
		Context:                  ctx,
	}

	clt, err := client.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to teleport proxy %s: %w", c.ProxyAddress, err)
	}
	return clt, nil
}

// credentials maps a Config to the Teleport SDK's Credentials slice.
func (c Config) credentials(ctx context.Context) ([]client.Credentials, error) {
	switch {
	case c.UseLocalProfile:
		// Empty dir => SDK default ~/.tsh. The profile is matched by the
		// proxy host derived from proxy_address.
		return []client.Credentials{client.LoadProfile("", profileNameFromProxy(c.ProxyAddress))}, nil

	case c.IdentityFilePath != "":
		return []client.Credentials{client.LoadIdentityFile(c.IdentityFilePath)}, nil

	case c.IdentityFileData != "":
		return []client.Credentials{client.LoadIdentityFileFromString(c.IdentityFileData)}, nil

	case c.JoinMethod != "":
		// Delegated OIDC-family join: fetch the platform identity token and
		// exchange it with the proxy's JoinService for short-lived certs.
		return c.credentialsFromJoin(ctx)
	}
	return nil, errors.New("no credentials configured")
}

// profileNameFromProxy extracts the host portion of a host:port string.
// Profile files in ~/.tsh are named after the proxy host, e.g.
// `~/.tsh/teleport.example.com.yaml`. Uses net.SplitHostPort so IPv6
// literals (e.g. "[::1]:443") are handled correctly; falls back to the raw
// address when there is no port.
func profileNameFromProxy(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
