package idtoken

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSupportedAndIsSupported(t *testing.T) {
	want := map[string]bool{"github": true, "gitlab": true, "kubernetes": true, "spacelift": true}
	got := Supported()
	if len(got) != len(want) {
		t.Fatalf("Supported() = %v, want %d methods", got, len(want))
	}
	for _, m := range got {
		if !want[m] {
			t.Fatalf("unexpected supported method %q", m)
		}
	}
	if IsSupported("iam") {
		t.Fatal("iam should not be supported")
	}
	if !IsSupported("GitHub") {
		t.Fatal("IsSupported should be case-insensitive")
	}
}

func TestFetchUnsupported(t *testing.T) {
	if _, err := Fetch(context.Background(), "iam", "aud"); err == nil {
		t.Fatal("expected error for unsupported method")
	}
}

func TestFetchGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer req-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.URL.Query().Get("audience"); got != "teleport.example.com" {
			t.Errorf("audience = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": "the-jwt"})
	}))
	defer srv.Close()

	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "req-token")

	tok, err := Fetch(context.Background(), "github", "teleport.example.com")
	if err != nil {
		t.Fatalf("Fetch github: %v", err)
	}
	if tok != "the-jwt" {
		t.Fatalf("token = %q, want the-jwt", tok)
	}
}

func TestFetchGitHubMissingEnv(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	if _, err := Fetch(context.Background(), "github", "aud"); err == nil {
		t.Fatal("expected error when GitHub OIDC env is unset")
	}
}

func TestFetchGitHubServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "req-token")
	if _, err := Fetch(context.Background(), "github", "aud"); err == nil {
		t.Fatal("expected error on non-200 from token endpoint")
	}
}

func TestFetchGitLab(t *testing.T) {
	t.Setenv("TELEPORT_GITLAB_ID_TOKEN_ENV", "")
	t.Setenv("CI_JOB_JWT_V2", "")
	t.Setenv("TELEPORT_ID_TOKEN", "gitlab-jwt")
	tok, err := Fetch(context.Background(), "gitlab", "aud")
	if err != nil {
		t.Fatalf("Fetch gitlab: %v", err)
	}
	if tok != "gitlab-jwt" {
		t.Fatalf("token = %q", tok)
	}
}

func TestFetchGitLabCustomEnv(t *testing.T) {
	t.Setenv("TELEPORT_ID_TOKEN", "")
	t.Setenv("CI_JOB_JWT_V2", "")
	t.Setenv("TELEPORT_GITLAB_ID_TOKEN_ENV", "MY_TOKEN")
	t.Setenv("MY_TOKEN", "custom-jwt")
	tok, err := Fetch(context.Background(), "gitlab", "aud")
	if err != nil {
		t.Fatalf("Fetch gitlab: %v", err)
	}
	if tok != "custom-jwt" {
		t.Fatalf("token = %q", tok)
	}
}

func TestFetchGitLabLegacyFallback(t *testing.T) {
	t.Setenv("TELEPORT_GITLAB_ID_TOKEN_ENV", "")
	t.Setenv("TELEPORT_ID_TOKEN", "")
	t.Setenv("CI_JOB_JWT_V2", "legacy-jwt")
	tok, err := Fetch(context.Background(), "gitlab", "aud")
	if err != nil {
		t.Fatalf("Fetch gitlab: %v", err)
	}
	if tok != "legacy-jwt" {
		t.Fatalf("token = %q", tok)
	}
}

func TestFetchGitLabMissing(t *testing.T) {
	t.Setenv("TELEPORT_GITLAB_ID_TOKEN_ENV", "")
	t.Setenv("TELEPORT_ID_TOKEN", "")
	t.Setenv("CI_JOB_JWT_V2", "")
	if _, err := Fetch(context.Background(), "gitlab", "aud"); err == nil {
		t.Fatal("expected error when no gitlab token is present")
	}
}

func TestFetchKubernetes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("k8s-jwt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEPORT_KUBERNETES_TOKEN_PATH", path)
	tok, err := Fetch(context.Background(), "kubernetes", "aud")
	if err != nil {
		t.Fatalf("Fetch kubernetes: %v", err)
	}
	if tok != "k8s-jwt" {
		t.Fatalf("token = %q (want trimmed k8s-jwt)", tok)
	}
}

func TestFetchKubernetesMissing(t *testing.T) {
	t.Setenv("TELEPORT_KUBERNETES_TOKEN_PATH", filepath.Join(t.TempDir(), "nope"))
	if _, err := Fetch(context.Background(), "kubernetes", "aud"); err == nil {
		t.Fatal("expected error when token file is absent")
	}
}

func TestFetchSpacelift(t *testing.T) {
	t.Setenv("SPACELIFT_OIDC_TOKEN", "spacelift-jwt")
	tok, err := Fetch(context.Background(), "spacelift", "aud")
	if err != nil {
		t.Fatalf("Fetch spacelift: %v", err)
	}
	if tok != "spacelift-jwt" {
		t.Fatalf("token = %q", tok)
	}
}

func TestFetchSpaceliftMissing(t *testing.T) {
	t.Setenv("SPACELIFT_OIDC_TOKEN", "")
	if _, err := Fetch(context.Background(), "spacelift", "aud"); err == nil {
		t.Fatal("expected error when SPACELIFT_OIDC_TOKEN is unset")
	}
}
