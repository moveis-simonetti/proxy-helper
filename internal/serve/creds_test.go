package serve

import (
	"os"
	"path/filepath"
	"testing"

	"proxy-helper/internal/proxy"
)

func writeSecret(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveFromFile(t *testing.T) {
	path := writeSecret(t, "from-file\n", 0o600)
	_, pass, deprecated, err := Resolve(proxy.Config{Username: "alice", PasswordFile: path})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if pass != "from-file" {
		t.Errorf("pass = %q, want %q (trailing newline must be trimmed)", pass, "from-file")
	}
	if deprecated {
		t.Error("password_file must not be reported as deprecated")
	}
}

func TestResolveRejectsLoosePermissions(t *testing.T) {
	path := writeSecret(t, "nope", 0o644)
	if _, _, _, err := Resolve(proxy.Config{PasswordFile: path}); err == nil {
		t.Fatal("expected an error for a world-readable password file")
	}
}

func TestResolvePrecedence(t *testing.T) {
	path := writeSecret(t, "from-file", 0o600)
	t.Setenv("PROFILE_VAR", "from-profile-env")
	t.Setenv("PROXY_HELPER_PASSWORD", "from-global-env")

	// file beats everything
	if _, pass, _, _ := Resolve(proxy.Config{
		PasswordFile: path, PasswordEnv: "PROFILE_VAR", Password: "legacy",
	}); pass != "from-file" {
		t.Errorf("file should win, got %q", pass)
	}
	// profile env beats global env and legacy
	if _, pass, _, _ := Resolve(proxy.Config{
		PasswordEnv: "PROFILE_VAR", Password: "legacy",
	}); pass != "from-profile-env" {
		t.Errorf("profile env should win, got %q", pass)
	}
	// global env beats legacy
	if _, pass, _, _ := Resolve(proxy.Config{Password: "legacy"}); pass != "from-global-env" {
		t.Errorf("global env should win, got %q", pass)
	}
}

func TestResolveLegacyIsDeprecated(t *testing.T) {
	t.Setenv("PROXY_HELPER_PASSWORD", "")
	_, pass, deprecated, err := Resolve(proxy.Config{Password: "legacy"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if pass != "legacy" {
		t.Errorf("pass = %q, want legacy", pass)
	}
	if !deprecated {
		t.Error("legacy pass field must be reported as deprecated")
	}
}

func TestResolveNoPassword(t *testing.T) {
	t.Setenv("PROXY_HELPER_PASSWORD", "")
	_, pass, _, err := Resolve(proxy.Config{Username: "alice"})
	if err != nil {
		t.Fatalf("a missing password is not an error: %v", err)
	}
	if pass != "" {
		t.Errorf("pass = %q, want empty", pass)
	}
}
