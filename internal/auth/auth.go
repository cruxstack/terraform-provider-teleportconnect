// Package auth builds an authenticated Teleport API client from provider
// configuration. Each supported auth method maps to a different Credentials
// implementation in the upstream Teleport SDK.
package auth

import (
	"context"
	"errors"
	"fmt"
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
	JoinMethod       string
	JoinToken        string

	// InsecureSkipVerify disables proxy TLS verification. Should never
	// be true outside of local development against a self-signed cluster.
	InsecureSkipVerify bool
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

	if (c.JoinMethod != "") != (c.JoinToken != "") {
		return errors.New("join_method and join_token must be set together")
	}

	return nil
}

// Build constructs a connected *client.Client for the configured auth mode.
// The caller owns the returned client and must Close() it.
func Build(ctx context.Context, c Config) (*client.Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	creds, err := c.credentials()
	if err != nil {
		return nil, err
	}

	cfg := client.Config{
		Addrs:                    []string{c.ProxyAddress},
		Credentials:              creds,
		InsecureAddressDiscovery: c.InsecureSkipVerify,
		Context:                  ctx,
	}

	clt, err := client.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to teleport proxy %s: %w", c.ProxyAddress, err)
	}
	return clt, nil
}

// credentials maps a Config to the Teleport SDK's Credentials slice. Step 2
// of the build plan covers identity_file_path and use_local_profile only;
// Step 6 fills in the rest.
func (c Config) credentials() ([]client.Credentials, error) {
	switch {
	case c.UseLocalProfile:
		// Empty dir/name => SDK defaults: dir=~/.tsh, name=active profile.
		// If the operator wants a specific profile, they set proxy_address
		// to that proxy's host:port; the SDK matches the profile by host.
		return []client.Credentials{client.LoadProfile("", profileNameFromProxy(c.ProxyAddress))}, nil

	case c.IdentityFilePath != "":
		return []client.Credentials{client.LoadIdentityFile(c.IdentityFilePath)}, nil

	case c.IdentityFileData != "":
		return []client.Credentials{client.LoadIdentityFileFromString(c.IdentityFileData)}, nil

	case c.JoinMethod != "":
		// Delegated joins (iam, github, gcp, spacelift, kubernetes, ...)
		// require multi-step challenge protocols and an unauthenticated
		// gRPC connection to the proxy's JoinService. The clean path
		// for that is gravitational/teleport's lib/tbot, which is
		// AGPL and lives outside the api/ module - we can't link it
		// from this provider.
		//
		// For non-interactive runtimes today, the recommended workflow:
		//   1. Run tbot (or the official teleportmwi provider) as a
		//      sidecar that writes an identity file.
		//   2. Set identity_file_path or identity_file_data on this
		//      provider to consume that identity.
		//
		// Future work: reimplement the IAM (and a couple of token-based)
		// methods directly against api/client/joinservice.go. Tracked as
		// a follow-up; this prototype focuses on the access surface.
		return nil, fmt.Errorf("join_method=%q is not yet implemented in this provider; use identity_file_path or identity_file_data instead (e.g. from `tctl auth sign` or a sidecar `tbot`)", c.JoinMethod)
	}
	return nil, errors.New("no credentials configured")
}

// profileNameFromProxy extracts the host portion of a host:port string.
// Profile files in ~/.tsh are named after the proxy host, e.g.
// `~/.tsh/use1-common-teleport.tools.myprize.io.yaml`.
func profileNameFromProxy(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}
