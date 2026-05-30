// Package idtoken fetches the OIDC/JWT identity token that a CI platform
// exposes to its jobs. The token is then presented to Teleport's JoinService
// via the delegated (OIDC-family) join methods, removing the need for a
// pre-provisioned identity file or a sidecar tbot process.
//
// Only the join methods this provider supports are implemented here:
// github, gitlab, kubernetes, and spacelift.
package idtoken

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Fetch returns the identity token for the given join method. audience is the
// expected audience claim the token must carry; some platforms require it to
// be requested explicitly (e.g. GitHub), others ignore it.
func Fetch(ctx context.Context, method, audience string) (string, error) {
	f, ok := fetchers[strings.ToLower(strings.TrimSpace(method))]
	if !ok {
		return "", fmt.Errorf("join_method %q is not supported; supported methods: %s", method, strings.Join(Supported(), ", "))
	}
	token, err := f(ctx, audience)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("join_method %q produced an empty identity token", method)
	}
	return token, nil
}

// Supported returns the sorted list of supported join methods.
func Supported() []string {
	out := make([]string, 0, len(fetchers))
	for k := range fetchers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsSupported reports whether the given join method has a token fetcher.
func IsSupported(method string) bool {
	_, ok := fetchers[strings.ToLower(strings.TrimSpace(method))]
	return ok
}

type fetcher func(ctx context.Context, audience string) (string, error)

// fetchers is the single source of truth for supported join methods.
var fetchers = map[string]fetcher{
	"github":     fetchGitHub,
	"gitlab":     fetchGitLab,
	"kubernetes": fetchKubernetes,
	"spacelift":  fetchSpacelift,
}
