package idtoken

import (
	"context"
	"fmt"
	"os"
)

// fetchSpacelift reads the Spacelift OIDC token from the environment. Spacelift
// injects $SPACELIFT_OIDC_TOKEN into every run; its audience is the Spacelift
// account's hostname (e.g. <account>.app.spacelift.io), which the Teleport
// spacelift join token must be configured to expect.
func fetchSpacelift(_ context.Context, _ string) (string, error) {
	if v := os.Getenv("SPACELIFT_OIDC_TOKEN"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("spacelift join: $SPACELIFT_OIDC_TOKEN not set; this method only works inside a Spacelift run")
}
