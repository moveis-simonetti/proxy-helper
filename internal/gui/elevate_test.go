// No "gui" build tag, matching elevate.go: these tests run under plain
// `go test ./...`, with no display and no real pkexec/polkit prompt.
package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestElevateCmd(t *testing.T) {
	got := elevateCmd("/opt/proxy-helper/proxy-helper", "work", []string{"apt", "dockerd"}, "/home/alice/.config")
	want := []string{
		"pkexec", "env", "XDG_CONFIG_HOME=/home/alice/.config",
		"/opt/proxy-helper/proxy-helper", "proxy", "set",
		"--profile", "work",
		"--targets", "apt,dockerd",
	}
	if !equalArgs(got, want) {
		t.Fatalf("elevateCmd() = %v, want %v", got, want)
	}
}

// TestElevateCmdNeverAddsViaLocal covers the fix for the finding that
// --via-local, when threaded into the pkexec'd CLI, made every privileged
// apply fail: under pkexec, DaemonActive() always reports false (no
// XDG_RUNTIME_DIR, no user systemd manager), so app.Apply returns
// ErrDaemonNotRunning even though the daemon is actually running. The
// caller now refuses the "Via daemon local" + privileged-targets
// combination before ever building this command, and elevateCmd itself has
// no way to add the flag any more — this just pins that down.
func TestElevateCmdNeverAddsViaLocal(t *testing.T) {
	got := elevateCmd("/opt/proxy-helper/proxy-helper", "work", []string{"apt"}, "/home/alice/.config")
	for _, arg := range got {
		if arg == "--via-local" {
			t.Fatalf("elevateCmd() must never include --via-local, got %v", got)
		}
	}
}

// TestSplitSessionAwareKeepsSessionScopedOutOfPrivileged is the regression
// test for the defect where gnome (RequiresRoot()==true on a system with
// the PackageKit cache, but session-scoped) was sent through pkexec, where
// gsettings cannot reach the user's D-Bus session at all. A session-scoped
// target with Root==true must land in "user", never in "privileged",
// regardless of Root.
func TestSplitSessionAwareKeepsSessionScopedOutOfPrivileged(t *testing.T) {
	user, privileged := splitSessionAware([]selectedTargetInfo{
		{Name: "gnome", Root: true, SessionScoped: true},
		{Name: "kde", Root: false, SessionScoped: true},
		{Name: "apt", Root: true, SessionScoped: false},
		{Name: "git", Root: false, SessionScoped: false},
	})

	if !equalArgs(privileged, []string{"apt"}) {
		t.Errorf("privileged = %v, want [apt]", privileged)
	}
	if !equalArgs(user, []string{"gnome", "kde", "git"}) {
		t.Errorf("user = %v, want [gnome kde git]", user)
	}
}

// TestResolveConfigHome ties resolveConfigHome to os.UserConfigDir()'s own
// XDG_CONFIG_HOME handling, since elevateCmd's whole fix depends on that
// value being the same one the GUI process would resolve its own config
// under.
func TestResolveConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/proxy-helper-test-config-home")

	got, err := resolveConfigHome()
	if err != nil {
		t.Fatalf("resolveConfigHome: %v", err)
	}
	if got != "/tmp/proxy-helper-test-config-home" {
		t.Errorf("resolveConfigHome() = %q, want %q", got, "/tmp/proxy-helper-test-config-home")
	}
}

// TestElevateViaLocalCmdsBridgeDisabled covers case (b): "Via daemon local"
// on, Docker bridge off. One elevated call, loopback for every privileged
// target, --host/--port/--no-proxy explicit instead of --profile.
func TestElevateViaLocalCmdsBridgeDisabled(t *testing.T) {
	got := elevateViaLocalCmds(
		"/opt/proxy-helper/proxy-helper", 8888,
		[]string{"localhost", "127.0.0.1"},
		[]string{"apt", "snap", "dockerd"},
		false, "",
		"/home/alice/.config",
	)
	want := [][]string{
		{
			"pkexec", "env", "XDG_CONFIG_HOME=/home/alice/.config",
			"/opt/proxy-helper/proxy-helper", "proxy", "set",
			"--host", "127.0.0.1",
			"--port", "8888",
			"--no-proxy", "localhost,127.0.0.1",
			"--targets", "apt,snap,dockerd",
		},
	}
	if !equalCmds(got, want) {
		t.Fatalf("elevateViaLocalCmds() = %v, want %v", got, want)
	}
}

// TestElevateViaLocalCmdsBridgeEnabledDockerdSelected covers case (c):
// "Via daemon local" on, Docker bridge on, dockerd among the selected
// privileged targets — two elevated calls, one for dockerd with the bridge
// address, one for the rest with loopback.
func TestElevateViaLocalCmdsBridgeEnabledDockerdSelected(t *testing.T) {
	got := elevateViaLocalCmds(
		"/opt/proxy-helper/proxy-helper", 8888,
		[]string{"localhost"},
		[]string{"apt", "snap", "dockerd"},
		true, "172.17.0.1",
		"/home/alice/.config",
	)
	want := [][]string{
		{
			"pkexec", "env", "XDG_CONFIG_HOME=/home/alice/.config",
			"/opt/proxy-helper/proxy-helper", "proxy", "set",
			"--host", "172.17.0.1",
			"--port", "8888",
			"--no-proxy", "localhost",
			"--targets", "dockerd",
		},
		{
			"pkexec", "env", "XDG_CONFIG_HOME=/home/alice/.config",
			"/opt/proxy-helper/proxy-helper", "proxy", "set",
			"--host", "127.0.0.1",
			"--port", "8888",
			"--no-proxy", "localhost",
			"--targets", "apt,snap",
		},
	}
	if !equalCmds(got, want) {
		t.Fatalf("elevateViaLocalCmds() = %v, want %v", got, want)
	}
}

// TestElevateViaLocalCmdsBridgeEnabledDockerdOnly covers the "skip the
// second call" case: dockerd is the only privileged target selected, so
// there is nothing left for a loopback call.
func TestElevateViaLocalCmdsBridgeEnabledDockerdOnly(t *testing.T) {
	got := elevateViaLocalCmds(
		"/opt/proxy-helper/proxy-helper", 8888,
		nil,
		[]string{"dockerd"},
		true, "172.17.0.1",
		"/home/alice/.config",
	)
	if len(got) != 1 {
		t.Fatalf("expected exactly one command when dockerd is the only privileged target, got %d: %v", len(got), got)
	}
	want := []string{
		"pkexec", "env", "XDG_CONFIG_HOME=/home/alice/.config",
		"/opt/proxy-helper/proxy-helper", "proxy", "set",
		"--host", "172.17.0.1",
		"--port", "8888",
		"--targets", "dockerd",
	}
	if !equalArgs(got[0], want) {
		t.Fatalf("elevateViaLocalCmds()[0] = %v, want %v", got[0], want)
	}
}

// TestElevateViaLocalCmdsBridgeEnabledNoDockerTarget covers Docker bridge
// enabled but no Docker target selected: still just one loopback call,
// since nothing needs the bridge address.
func TestElevateViaLocalCmdsBridgeEnabledNoDockerTarget(t *testing.T) {
	got := elevateViaLocalCmds(
		"/opt/proxy-helper/proxy-helper", 8888,
		nil,
		[]string{"apt", "snap"},
		true, "172.17.0.1",
		"/home/alice/.config",
	)
	if len(got) != 1 {
		t.Fatalf("expected exactly one command when no Docker target is selected, got %d: %v", len(got), got)
	}
	want := []string{
		"pkexec", "env", "XDG_CONFIG_HOME=/home/alice/.config",
		"/opt/proxy-helper/proxy-helper", "proxy", "set",
		"--host", "127.0.0.1",
		"--port", "8888",
		"--targets", "apt,snap",
	}
	if !equalArgs(got[0], want) {
		t.Fatalf("elevateViaLocalCmds()[0] = %v, want %v", got[0], want)
	}
}

// TestNeedsTwoElevatedCalls locks in the predicate apply() uses to decide
// whether to warn about a second polkit dialog before running anything.
func TestNeedsTwoElevatedCalls(t *testing.T) {
	cases := []struct {
		name         string
		privileged   []string
		dockerBridge bool
		want         bool
	}{
		{"bridge off", []string{"apt", "dockerd"}, false, false},
		{"bridge on, no dockerd", []string{"apt", "snap"}, true, false},
		{"bridge on, dockerd only", []string{"dockerd"}, true, false},
		{"bridge on, dockerd plus others", []string{"apt", "dockerd"}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsTwoElevatedCalls(c.privileged, c.dockerBridge); got != c.want {
				t.Errorf("needsTwoElevatedCalls(%v, %v) = %v, want %v", c.privileged, c.dockerBridge, got, c.want)
			}
		})
	}
}

func equalCmds(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalArgs(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestElevateUnsetCmd(t *testing.T) {
	got := elevateUnsetCmd("/opt/proxy-helper/proxy-helper", []string{"apt"})
	want := []string{
		"pkexec", "/opt/proxy-helper/proxy-helper", "proxy", "unset",
		"--targets", "apt",
	}
	if !equalArgs(got, want) {
		t.Fatalf("elevateUnsetCmd() = %v, want %v", got, want)
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestClassifyExit(t *testing.T) {
	cases := []struct {
		code          int
		wantCancelled bool
		wantErr       bool
	}{
		{code: 0, wantCancelled: false, wantErr: false},
		{code: 1, wantCancelled: false, wantErr: true},
		{code: 126, wantCancelled: true, wantErr: false},
		{code: 127, wantCancelled: false, wantErr: true},
	}
	for _, c := range cases {
		cancelled, err := classifyExit(c.code)
		if cancelled != c.wantCancelled {
			t.Errorf("classifyExit(%d): cancelled = %v, want %v", c.code, cancelled, c.wantCancelled)
		}
		if (err != nil) != c.wantErr {
			t.Errorf("classifyExit(%d): err = %v, wantErr = %v", c.code, err, c.wantErr)
		}
	}
}

// TestRunCmd drives runCmd against "sh -c ..." rather than a real pkexec, as
// the brief requires: no test here ever shells out to pkexec or waits on a
// real polkit dialog.
func TestRunCmd(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		_, cancelled, err := runCmd([]string{"sh", "-c", "exit 126"})
		if !cancelled {
			t.Error("expected cancelled = true for exit 126")
		}
		if err != nil {
			t.Errorf("expected no error for a cancelled run, got %v", err)
		}
	})

	t.Run("pkexec could not execute", func(t *testing.T) {
		_, cancelled, err := runCmd([]string{"sh", "-c", "exit 127"})
		if cancelled {
			t.Error("expected cancelled = false for exit 127")
		}
		if err == nil {
			t.Error("expected an error for exit 127")
		}
	})

	t.Run("cli failure", func(t *testing.T) {
		_, cancelled, err := runCmd([]string{"sh", "-c", "echo boom >&2; exit 1"})
		if cancelled {
			t.Error("expected cancelled = false for exit 1")
		}
		if err == nil {
			t.Error("expected an error for exit 1")
		}
	})

	t.Run("success", func(t *testing.T) {
		out, cancelled, err := runCmd([]string{"sh", "-c", "echo hi"})
		if cancelled {
			t.Error("expected cancelled = false for exit 0")
		}
		if err != nil {
			t.Fatalf("expected no error for exit 0, got %v", err)
		}
		if strings.TrimSpace(out) != "hi" {
			t.Errorf("expected combined output %q, got %q", "hi", out)
		}
	})

	// elevateCmd wraps the reinvoked CLI as "pkexec env VAR=val binary
	// ...": this covers the "env VAR=val cmd..." shape runCmd actually
	// executes (minus pkexec itself, which cannot be exercised here), to
	// make sure runCmd handles a command whose args[0] is "env" the same
	// way it handles any other command.
	t.Run("env-wrapped command", func(t *testing.T) {
		out, cancelled, err := runCmd([]string{"env", "FOO=bar", "sh", "-c", "echo $FOO"})
		if cancelled {
			t.Error("expected cancelled = false for exit 0")
		}
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if strings.TrimSpace(out) != "bar" {
			t.Errorf("expected the env var to reach the wrapped command, got %q", out)
		}
	})

	t.Run("binary not found", func(t *testing.T) {
		_, cancelled, err := runCmd([]string{"proxy-helper-definitely-does-not-exist"})
		if cancelled {
			t.Error("expected cancelled = false when the command cannot even start")
		}
		if err == nil {
			t.Error("expected an error when the command cannot even start")
		}
	})
}

func TestFindCLIBinaryOnPath(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, cliBinaryName)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := findCLIBinary()
	if err != nil {
		t.Fatalf("findCLIBinary: %v", err)
	}
	// exec.LookPath may resolve symlinks/return an absolute path built
	// differently on some platforms; comparing the resolved file is more
	// robust than comparing strings.
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat(%q): %v", got, err)
	}
	wantInfo, err := os.Stat(fake)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Errorf("findCLIBinary() = %q, want the fake binary at %q", got, fake)
	}
}

func TestFindCLIBinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	if _, err := findCLIBinary(); err == nil {
		t.Error("expected an error when the CLI binary is nowhere to be found")
	}
}
