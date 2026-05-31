package provider

import (
	"fmt"
	"net"

	"github.com/gravitational/teleport/api/defaults"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/auth"
	"github.com/cruxstack/terraform-provider-teleportconnect/internal/tunnel"
)

// tunnelUpgradeMode translates the provider-level ALPN upgrade mode into the
// tunnel package's mode enum.
func tunnelUpgradeMode(m ALPNConnUpgradeMode) tunnel.ALPNUpgradeMode {
	switch m {
	case ALPNYes:
		return tunnel.ALPNUpgradeYes
	case ALPNNo:
		return tunnel.ALPNUpgradeNo
	default:
		return tunnel.ALPNUpgradeAuto
	}
}

// authUpgradeMode translates the provider-level ALPN upgrade mode into the
// auth package's mode enum.
func authUpgradeMode(m ALPNConnUpgradeMode) auth.ALPNUpgradeMode {
	switch m {
	case ALPNYes:
		return auth.ALPNUpgradeYes
	case ALPNNo:
		return auth.ALPNUpgradeNo
	default:
		return auth.ALPNUpgradeAuto
	}
}

// stringOrDefault returns v if it is non-empty, otherwise def.
func stringOrDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// labelsMatch returns true if every key/value in want is present in have
// with the same value. An empty want returns true.
func labelsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if hv, ok := have[k]; !ok || hv != v {
			return false
		}
	}
	return true
}

// hasPort reports whether addr appears to contain a port suffix that
// net.SplitHostPort can parse.
func hasPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}

// proxyHostPort splits a Teleport proxy address into host and port. The
// address is validated at provider configure time, so an address with no
// port is treated as host-only and defaults to the standard HTTPS port.
func proxyHostPort(addr string) (host string, port int, err error) {
	if !hasPort(addr) {
		return addr, defaults.StandardHTTPSPort, nil
	}
	h, p, _ := net.SplitHostPort(addr)
	parsedPort, portErr := net.LookupPort("tcp", p)
	if portErr != nil {
		return "", 0, fmt.Errorf("invalid proxy port %q: %w", p, portErr)
	}
	return h, parsedPort, nil
}
