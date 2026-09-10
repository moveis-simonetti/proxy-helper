package proxy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeEtcEnvironment points the target at a throwaway /etc/environment.
func withFakeEtcEnvironment(t *testing.T, initial string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "environment")
	if initial != "" {
		if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig := etcEnvironmentPath
	etcEnvironmentPath = path
	t.Cleanup(func() { etcEnvironmentPath = orig })
	return path
}

func TestSystemEnvRequiresRoot(t *testing.T) {
	if !NewSystemEnvTarget().RequiresRoot() {
		t.Error("system-env writes /etc/environment; it must require root")
	}
}

// pam_env parses /etc/environment itself — it is not sourced by a shell — so
// the lines must be bare KEY=value: no "export", and no surrounding quotes,
// which pam_env would keep as part of the value.
func TestSystemEnvRenderIsBareAssignments(t *testing.T) {
	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888", NoProxy: []string{"localhost", "10.0.0.0/8"}}
	body, err := renderSystemEnvBlock(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if strings.Contains(body, "export ") {
		t.Errorf("body has 'export':\n%s", body)
	}
	if strings.Contains(body, `"`) {
		t.Errorf("body has quotes:\n%s", body)
	}
	for _, want := range []string{
		"HTTP_PROXY=http://127.0.0.1:8888",
		"http_proxy=http://127.0.0.1:8888",
		"HTTPS_PROXY=http://127.0.0.1:8888",
		"https_proxy=http://127.0.0.1:8888",
		"NO_PROXY=localhost,10.0.0.0/8",
		"no_proxy=localhost,10.0.0.0/8",
		"NODE_USE_ENV_PROXY=1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestSystemEnvRenderOmitsNoProxyWhenEmpty(t *testing.T) {
	body, err := renderSystemEnvBlock(Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(body, "NO_PROXY") || strings.Contains(body, "no_proxy") {
		t.Errorf("empty no-proxy should emit no line:\n%s", body)
	}
}

// The file already carries PATH and often more; the target owns only its
// marked block and must leave every other line untouched.
func TestSystemEnvSetPreservesExistingLines(t *testing.T) {
	withFakeEtcEnvironment(t, "PATH=\"/usr/local/sbin:/usr/bin\"\nLANG=en_US.UTF-8\n")

	var out bytes.Buffer
	ex := &Executor{DryRun: true, Out: &out}
	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := NewSystemEnvTarget().Set(ex, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}

	preview := out.String()
	for _, want := range []string{
		`PATH="/usr/local/sbin:/usr/bin"`,
		"LANG=en_US.UTF-8",
		"HTTP_PROXY=http://127.0.0.1:8888",
		blockBegin,
	} {
		if !strings.Contains(preview, want) {
			t.Errorf("dry-run preview missing %q:\n%s", want, preview)
		}
	}
}

func TestSystemEnvUnsetRemovesOnlyTheBlock(t *testing.T) {
	body, _ := renderSystemEnvBlock(Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"})
	initial := "PATH=/usr/bin\n\n" + blockBegin + "\n" + strings.TrimRight(body, "\n") + "\n" + blockEnd + "\n"
	withFakeEtcEnvironment(t, initial)

	var out bytes.Buffer
	ex := &Executor{DryRun: true, Out: &out}
	if err := NewSystemEnvTarget().Unset(ex); err != nil {
		t.Fatalf("Unset: %v", err)
	}

	preview := out.String()
	if !strings.Contains(preview, "PATH=/usr/bin") {
		t.Errorf("Unset dropped an unmanaged line:\n%s", preview)
	}
	if strings.Contains(preview, "HTTP_PROXY=") {
		t.Errorf("Unset left the proxy block behind:\n%s", preview)
	}
}

// Unsetting a file that never had the block is a no-op, like every other
// target — no write, no error.
func TestSystemEnvUnsetIsNoOpWithoutBlock(t *testing.T) {
	withFakeEtcEnvironment(t, "PATH=/usr/bin\n")

	var out bytes.Buffer
	ex := &Executor{DryRun: true, Out: &out}
	if err := NewSystemEnvTarget().Unset(ex); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if strings.Contains(out.String(), "would") {
		t.Errorf("Unset without a block should not write:\n%s", out.String())
	}
}

func TestSystemEnvStatusReadsBlock(t *testing.T) {
	body, _ := renderSystemEnvBlock(Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"})
	withFakeEtcEnvironment(t, blockBegin+"\n"+strings.TrimRight(body, "\n")+"\n"+blockEnd+"\n")

	st, err := NewSystemEnvTarget().Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Enabled {
		t.Errorf("Enabled = false, want true")
	}
	if !strings.Contains(st.Detail, "http://127.0.0.1:8888") {
		t.Errorf("Detail = %q, want the proxy URL", st.Detail)
	}
}

func TestSystemEnvStatusNotSet(t *testing.T) {
	withFakeEtcEnvironment(t, "PATH=/usr/bin\n")

	st, err := NewSystemEnvTarget().Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Enabled {
		t.Errorf("Enabled = true on a file with no block")
	}
	if st.Detail != "not set" {
		t.Errorf("Detail = %q, want %q", st.Detail, "not set")
	}
}

func TestSystemEnvAvailableFollowsParentDir(t *testing.T) {
	withFakeEtcEnvironment(t, "")
	if !NewSystemEnvTarget().Available() {
		t.Error("Available() = false, want true when the parent dir exists")
	}

	orig := etcEnvironmentPath
	etcEnvironmentPath = filepath.Join(t.TempDir(), "nope", "environment")
	t.Cleanup(func() { etcEnvironmentPath = orig })
	if NewSystemEnvTarget().Available() {
		t.Error("Available() = true, want false when the parent dir is missing")
	}
}
