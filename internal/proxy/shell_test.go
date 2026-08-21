package proxy

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeHome points the shell target at a throwaway HOME so tests never
// touch the developer's real ~/.bashrc.
func withFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# existing content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestShellSetExportsNodeProxySupport(t *testing.T) {
	home := withFakeHome(t)

	cfg := Config{Scheme: "http", Host: "proxy.corp", Port: "3128", NoProxy: []string{"localhost"}}
	if err := (&shellTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	// Node's fetch ignores HTTP_PROXY unless this is set, so a shell that
	// exports only the classic variables leaves every Node CLI broken.
	if !strings.Contains(got, "export NODE_USE_ENV_PROXY=1") {
		t.Errorf("NODE_USE_ENV_PROXY missing; Node CLIs would fail with ENETUNREACH:\n%s", got)
	}
	for _, want := range []string{"HTTP_PROXY", "https_proxy", "NO_PROXY"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	if !strings.Contains(got, "# existing content") {
		t.Error("the rc file's original content was destroyed")
	}
}

func TestShellUnsetRemovesNodeProxySupport(t *testing.T) {
	home := withFakeHome(t)
	ex := &Executor{}
	cfg := Config{Scheme: "http", Host: "proxy.corp", Port: "3128"}

	if err := (&shellTarget{}).Set(ex, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := (&shellTarget{}).Unset(ex); err != nil {
		t.Fatalf("Unset: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	// The variable is only correct while a proxy is configured; leaving it
	// behind would be litter in the user's shell.
	if strings.Contains(got, "NODE_USE_ENV_PROXY") {
		t.Errorf("NODE_USE_ENV_PROXY survived unset:\n%s", got)
	}
	if !strings.Contains(got, "# existing content") {
		t.Error("unset destroyed the rc file's original content")
	}
}

// TestShellDryRunShowsOnlyTheManagedBlock is a privacy regression test. A dry
// run used to print the whole rc file, which in a real session leaked the
// user's unrelated API tokens (GITHUB_TOKEN, OPENAI_API_KEY, ...) into command
// output. Only the block this tool owns may be shown.
func TestShellDryRunShowsOnlyTheManagedBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rc := filepath.Join(home, ".bashrc")
	secret := "export OPENAI_API_KEY=sk-do-not-print-me"
	if err := os.WriteFile(rc, []byte(secret+"\nexport PATH=$PATH:/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureOutput(t, func() {
		cfg := Config{Scheme: "http", Host: "proxy.corp", Port: "3128"}
		if err := (&shellTarget{}).Set(&Executor{DryRun: true}, cfg); err != nil {
			t.Fatalf("Set: %v", err)
		}
	})

	if strings.Contains(out, "sk-do-not-print-me") {
		t.Errorf("the dry-run leaked an unrelated secret from the rc file:\n%s", out)
	}
	if !strings.Contains(out, "HTTP_PROXY") {
		t.Errorf("the dry-run should still show what it would change:\n%s", out)
	}

	// And it must not have written anything.
	data, _ := os.ReadFile(rc)
	if strings.Contains(string(data), "HTTP_PROXY") {
		t.Error("dry-run wrote to the file")
	}
}

func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}
