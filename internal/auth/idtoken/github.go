package idtoken

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// fetchGitHub requests an OIDC token from the GitHub Actions token service.
// The workflow must grant `permissions: id-token: write` (which exposes the
// ACTIONS_ID_TOKEN_REQUEST_* env vars). audience, when set, becomes the
// requested `aud` claim.
func fetchGitHub(ctx context.Context, audience string) (string, error) {
	reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if reqURL == "" || reqToken == "" {
		return "", fmt.Errorf("github join: ACTIONS_ID_TOKEN_REQUEST_URL/ACTIONS_ID_TOKEN_REQUEST_TOKEN not set; ensure the workflow grants `permissions: id-token: write`")
	}

	if audience != "" {
		u, err := url.Parse(reqURL)
		if err != nil {
			return "", fmt.Errorf("github join: parsing token request URL: %w", err)
		}
		q := u.Query()
		q.Set("audience", audience)
		u.RawQuery = q.Encode()
		reqURL = u.String()
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("github join: building token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+reqToken)
	req.Header.Set("Accept", "application/json; api-version=2.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github join: requesting id token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("github join: reading id token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github join: token endpoint returned %s: %s", resp.Status, string(body))
	}

	var payload struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("github join: decoding id token response: %w", err)
	}
	return payload.Value, nil
}
