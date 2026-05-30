package idtoken

import (
	"context"
	"fmt"
	"os"
)

// gitlabTokenEnvVar is the default id_tokens var the GitLab job must declare
// (with the audience set there). Override with TELEPORT_GITLAB_ID_TOKEN_ENV.
const gitlabTokenEnvVar = "TELEPORT_ID_TOKEN"

// fetchGitLab reads the GitLab CI OIDC token from the environment; the
// audience is configured in the job's id_tokens block, not here.
func fetchGitLab(_ context.Context, _ string) (string, error) {
	envName := os.Getenv("TELEPORT_GITLAB_ID_TOKEN_ENV")
	if envName == "" {
		envName = gitlabTokenEnvVar
	}
	if v := os.Getenv(envName); v != "" {
		return v, nil
	}
	if v := os.Getenv("CI_JOB_JWT_V2"); v != "" { // legacy GitLab
		return v, nil
	}
	return "", fmt.Errorf(
		"gitlab join: no ID token found in $%s (or legacy $CI_JOB_JWT_V2); declare an id_tokens entry in your job with aud set to the join token's audience, and set TELEPORT_GITLAB_ID_TOKEN_ENV if it is named differently",
		envName,
	)
}
