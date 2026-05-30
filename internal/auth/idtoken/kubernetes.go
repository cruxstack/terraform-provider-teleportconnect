package idtoken

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// defaultK8sTokenPath is the in-pod path of the default service account token.
// For Teleport's kubernetes join with a bound audience, mount a projected
// service account token (with the audience set to the join token's audience)
// and point TELEPORT_KUBERNETES_TOKEN_PATH at it.
const defaultK8sTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// fetchKubernetes reads the pod's service account JWT from disk. The path can
// be overridden with TELEPORT_KUBERNETES_TOKEN_PATH, which is the recommended
// way to consume a projected token whose audience matches the join token
// (the default token's audience is the API server, which only works for
// in-cluster kubernetes join, not JWKS/static join).
func fetchKubernetes(_ context.Context, _ string) (string, error) {
	path := os.Getenv("TELEPORT_KUBERNETES_TOKEN_PATH")
	if path == "" {
		path = defaultK8sTokenPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("kubernetes join: reading service account token at %s: %w (set TELEPORT_KUBERNETES_TOKEN_PATH to a projected token whose audience matches the join token)", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
