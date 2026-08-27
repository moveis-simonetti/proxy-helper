package serve

import (
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

// withFakeUnprotect stands in for the OS crypto so the precedence rules can
// be tested on the platform this project is developed on. It restores the
// real implementation afterwards.
func withFakeUnprotect(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	original := unprotect
	unprotect = fn
	t.Cleanup(func() { unprotect = original })
}

func TestResolvePrefersTheProtectedPassword(t *testing.T) {
	withFakeUnprotect(t, func(blob string) (string, error) {
		if blob != "blob-guardado" {
			t.Errorf("unprotect got %q, want %q", blob, "blob-guardado")
		}
		return "senha-real", nil
	})

	// Every other source is also set: the protected one has to win, or a
	// machine that still has a stale plaintext file would keep using it.
	t.Setenv(GlobalPasswordEnv, "senha-do-ambiente")
	cfg := proxy.Config{
		Username:          "gestao",
		PasswordProtected: "blob-guardado",
		Password:          "senha-legada",
	}

	user, pass, deprecated, err := Resolve(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "gestao" {
		t.Errorf("user = %q, want %q", user, "gestao")
	}
	if pass != "senha-real" {
		t.Errorf("pass = %q, want %q", pass, "senha-real")
	}
	if deprecated {
		t.Error("deprecated = true; the protected source is the recommended one")
	}
}

func TestResolveFailsLoudlyWhenTheProtectedPasswordCannotBeRead(t *testing.T) {
	withFakeUnprotect(t, func(string) (string, error) {
		return "", errCannotRead{}
	})

	// The legacy plaintext field is present and would "work". Falling back
	// to it would silently use a password the user believed was replaced —
	// and on Windows, a failure here means the account changed, which the
	// person needs told, not worked around.
	cfg := proxy.Config{PasswordProtected: "blob", Password: "senha-legada"}

	_, pass, _, err := Resolve(cfg)
	if err == nil {
		t.Fatalf("expected an error, got pass %q", pass)
	}
	if pass != "" {
		t.Errorf("pass = %q, want empty on failure", pass)
	}
}

func TestResolveKeepsTheExistingSourcesWhenNoProtectedPasswordIsSet(t *testing.T) {
	// Guards against the new branch swallowing the Linux path.
	t.Setenv(GlobalPasswordEnv, "senha-do-ambiente")

	_, pass, deprecated, err := Resolve(proxy.Config{Username: "gestao"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass != "senha-do-ambiente" {
		t.Errorf("pass = %q, want %q", pass, "senha-do-ambiente")
	}
	if deprecated {
		t.Error("deprecated = true for the global env var, which is not the legacy field")
	}
}

func TestUnprotectPasswordExplainsItselfOffWindows(t *testing.T) {
	// The real implementation for this platform: a config written on
	// Windows must not fail with a cryptic error here.
	_, err := unprotectPassword("qualquer-coisa")
	if err == nil {
		t.Fatal("expected an error on a platform without DPAPI")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Errorf("error does not say where the password came from: %v", err)
	}
}

type errCannotRead struct{}

func (errCannotRead) Error() string { return "cannot read the stored password" }
