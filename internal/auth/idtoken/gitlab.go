package idtoken

import (
	"context"
	"fmt"
	"os"
)

// gitlabTokenEnvVar is the environment variable the provider reads the GitLab
// ID token from. GitLab CI populates it when the job declares an `id_tokens`
// block, e.g.:
//
//	job:
//	  id_tokens:
//	    TELEPORT_ID_TOKEN:
//	      aud: https://teleport.example.com
//
// The name can be overridden with TELEPORT_GITLAB_ID_TOKEN_ENV for pipelines
// that already use a differently-named id_tokens entry.
const gitlabTokenEnvVar = "TELEPORT_ID_TOKEN"

// fetchGitLab reads the GitLab CI OIDC token from the environment. GitLab does
// not have a request endpoint like GitHub; the token is injected as an
// environment variable named by the job's `id_tokens` block, so the audience
// is configured there rather than here.
func fetchGitLab(_ context.Context, _ string) (string, error) {
	envName := os.Getenv("TELEPORT_GITLAB_ID_TOKEN_ENV")
	if envName == "" {
		envName = gitlabTokenEnvVar
	}
	if v := os.Getenv(envName); v != "" {
		return v, nil
	}
	// Legacy fallback for older GitLab versions.
	if v := os.Getenv("CI_JOB_JWT_V2"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf(
		"gitlab join: no ID token found in $%s (or legacy $CI_JOB_JWT_V2); declare an id_tokens entry in your job with aud set to the join token's audience, and set TELEPORT_GITLAB_ID_TOKEN_ENV if it is named differently",
		envName,
	)
}
