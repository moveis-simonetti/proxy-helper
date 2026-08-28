package proxy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeConfigDir points os.UserConfigDir() at a throwaway directory so
// tests never touch the developer's real ~/.config/environment.d.
func withFakeConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

// fakeSystemctl drops executables named "systemctl" and
// "dbus-update-activation-environment" on PATH (as the first entry), so
// tests exercise the target without touching the developer's real session
// manager — set-environment against the live session would leak a proxy
// into every app the developer launches afterwards.
//
// "show-environment" prints showEnv; every other subcommand exits 0. Each
// invocation is appended, one shell-joined line per call, to the file
// whose path is returned, so a test can assert on what the target actually
// asked systemd to do.
func fakeSystemctl(t *testing.T, showEnv string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")

	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + shellQuote(log) + "\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = show-environment ]; then printf %s " + shellQuote(showEnv) + "; fi\n" +
		"done\n" +
		"exit 0\n"
	for _, name := range []string{"systemctl", "dbus-update-activation-environment"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// systemctlCalls returns the commands recorded by fakeSystemctl, one per
// line. A missing file means nothing was ever invoked.
func systemctlCalls(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

// TestSessionEnvSetWritesEnvironmentDFile pins the file format:
// environment.d is parsed by systemd's own generator, not by a shell, so
// the lines must be bare KEY=value. A stray "export" (copied from the
// shell target) or shell quoting would make systemd read the literal
// characters as part of the value.
func TestSessionEnvSetWritesEnvironmentDFile(t *testing.T) {
	dir := withFakeConfigDir(t)
	fakeSystemctl(t, "")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888", NoProxy: []string{"localhost", ".local"}}
	if err := NewSessionEnvTarget().Set(&Executor{Out: &bytes.Buffer{}}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "environment.d", sessionEnvFileName))
	if err != nil {
		t.Fatalf("reading the generated file: %v", err)
	}
	got := string(data)

	for _, want := range []string{
		"HTTP_PROXY=http://127.0.0.1:8888",
		"http_proxy=http://127.0.0.1:8888",
		"HTTPS_PROXY=http://127.0.0.1:8888",
		"https_proxy=http://127.0.0.1:8888",
		"NO_PROXY=localhost,.local",
		"no_proxy=localhost,.local",
		// Same reason the shell target sets it: Node's fetch ignores the
		// classic variables unless this one is present.
		"NODE_USE_ENV_PROXY=1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "export ") {
		t.Errorf("environment.d is not a shell script; \"export\" must not appear:\n%s", got)
	}
	if strings.Contains(got, "\"") {
		t.Errorf("systemd would take the quotes as part of the value:\n%s", got)
	}
}

// TestSessionEnvSetPropagatesToTheLiveSession pins the half that makes a
// "proxy set" worth anything today: writing environment.d alone only takes
// effect at the next login. Pushing the same variables into the running
// user manager (and the D-Bus activation environment, which is how part of
// the GNOME session launches apps) is what lets an app launched a second
// later already see the proxy.
func TestSessionEnvSetPropagatesToTheLiveSession(t *testing.T) {
	withFakeConfigDir(t)
	log := fakeSystemctl(t, "")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := NewSessionEnvTarget().Set(&Executor{Out: &bytes.Buffer{}}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}

	calls := systemctlCalls(t, log)
	if !strings.Contains(calls, "--user set-environment") {
		t.Errorf("expected the variables to be pushed into the running user manager, got:\n%s", calls)
	}
	if !strings.Contains(calls, "HTTPS_PROXY=http://127.0.0.1:8888") {
		t.Errorf("expected the proxy URL in the set-environment call, got:\n%s", calls)
	}
	// dbus-update-activation-environment is logged through the same script.
	if !strings.Contains(calls, "--systemd HTTP_PROXY") {
		t.Errorf("expected the D-Bus activation environment to be updated too, got:\n%s", calls)
	}
}

// TestSessionEnvUnsetClearsBothLayers is the mirror of Set: leaving either
// half behind is a silent trap. A surviving file re-applies the proxy at
// the next login; surviving live variables keep pointing apps at a daemon
// the user just turned off.
func TestSessionEnvUnsetClearsBothLayers(t *testing.T) {
	dir := withFakeConfigDir(t)
	log := fakeSystemctl(t, "")
	ex := &Executor{Out: &bytes.Buffer{}}

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := NewSessionEnvTarget().Set(ex, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := NewSessionEnvTarget().Unset(ex); err != nil {
		t.Fatalf("Unset: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "environment.d", sessionEnvFileName)); !os.IsNotExist(err) {
		t.Errorf("the drop-in survived Unset; the proxy would come back at the next login (err=%v)", err)
	}
	if !strings.Contains(systemctlCalls(t, log), "--user unset-environment") {
		t.Errorf("expected the live variables to be cleared, got:\n%s", systemctlCalls(t, log))
	}
}

// TestSessionEnvUnsetToleratesAMissingFile guards the common case of
// unsetting a target that was never set: every other target treats that as
// a no-op, and a hard error here would fail a whole "proxy unset" run.
func TestSessionEnvUnsetToleratesAMissingFile(t *testing.T) {
	withFakeConfigDir(t)
	fakeSystemctl(t, "")

	if err := NewSessionEnvTarget().Unset(&Executor{Out: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Unset on a never-set target must be a no-op, got: %v", err)
	}
}

// TestSessionEnvUnavailableWithoutAUserManager keeps the target quiet where
// it makes no sense — a headless server, a container, a plain SSH session.
// Availability is environment detection, never an error: an unavailable
// target is skipped, exactly like kde and lxd on a machine without them.
func TestSessionEnvUnavailableWithoutAUserManager(t *testing.T) {
	// An empty PATH means "systemctl" cannot be found at all.
	t.Setenv("PATH", t.TempDir())

	if NewSessionEnvTarget().Available() {
		t.Error("session-env must report itself unavailable when there is no systemd --user to talk to")
	}
}

func TestSessionEnvSessionScoped(t *testing.T) {
	tgt := NewSessionEnvTarget()
	// Same contract as gnome: this target talks to the invoking user's
	// session, so a caller must never route it through pkexec/sudo — as
	// root there is no user manager holding the desktop's environment.
	if !tgt.SessionScoped() {
		t.Error("session-env must report SessionScoped() == true")
	}
	if tgt.RequiresRoot() {
		t.Error("session-env writes only under the user's own ~/.config; it must not require root")
	}
}

// TestSessionEnvStatusReportsTheLiveValue pins where Status reads from: the
// running session, not the file. The file says what the *next* login will
// get; the live value says what apps launched right now actually receive,
// which is what the user is troubleshooting.
func TestSessionEnvStatusReportsTheLiveValue(t *testing.T) {
	withFakeConfigDir(t)
	fakeSystemctl(t, "HTTPS_PROXY=http://127.0.0.1:8888\nLANG=pt_BR.UTF-8\n")

	st, err := NewSessionEnvTarget().Status(&Executor{Out: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Enabled {
		t.Error("a session carrying HTTPS_PROXY must report Enabled")
	}
	if !strings.Contains(st.Detail, "http://127.0.0.1:8888") {
		t.Errorf("expected the live proxy URL in Detail, got: %q", st.Detail)
	}
}

func TestSessionEnvStatusReportsDisabledOnACleanSession(t *testing.T) {
	withFakeConfigDir(t)
	fakeSystemctl(t, "LANG=pt_BR.UTF-8\n")

	st, err := NewSessionEnvTarget().Status(&Executor{Out: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Enabled {
		t.Error("a session with no proxy variables must not report Enabled")
	}
}

// TestSessionEnvAvailableWithAUserManager is the other half of
// TestSessionEnvUnavailableWithoutAUserManager: without it, a target that
// simply always returns false would pass the negative test while never
// doing anything on a real desktop.
func TestSessionEnvAvailableWithAUserManager(t *testing.T) {
	fakeSystemctl(t, "LANG=pt_BR.UTF-8\n")

	if !NewSessionEnvTarget().Available() {
		t.Error("session-env must be available when systemd --user answers")
	}
}

// TestSessionEnvStatusFlagsAStalePort is the mitigation for this target's
// one real sharp edge. A process's environment is frozen at launch and
// cannot be rewritten from outside, so changing the daemon's port leaves
// every already-running GUI app talking to the old one. Worse, the GUI
// changes the port without re-applying the targets, so the live session
// can lag behind the profile until the user re-applies. Status must say so
// instead of letting it turn into a silent "1Password stopped working".
func TestSessionEnvStatusFlagsAStalePort(t *testing.T) {
	dir := withFakeConfigDir(t)
	// The session still carries the old port ...
	fakeSystemctl(t, "HTTPS_PROXY=http://127.0.0.1:8888\n")
	// ... while the profile has already moved to a new one.
	writeTestProfile(t, dir, 9090)

	st, err := NewSessionEnvTarget().Status(&Executor{Out: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(st.Detail, "9090") || !strings.Contains(st.Detail, "8888") {
		t.Errorf("Detail must name both the live port and the configured one, got: %q", st.Detail)
	}
	if !strings.Contains(st.Detail, "relaunch") {
		t.Errorf("Detail must tell the user what to do about it, got: %q", st.Detail)
	}
}

// TestSessionEnvStatusQuietWhenPortsAgree is the other half: the warning
// above is only useful if it stays absent in the normal case.
func TestSessionEnvStatusQuietWhenPortsAgree(t *testing.T) {
	dir := withFakeConfigDir(t)
	fakeSystemctl(t, "HTTPS_PROXY=http://127.0.0.1:8888\n")
	writeTestProfile(t, dir, 8888)

	st, err := NewSessionEnvTarget().Status(&Executor{Out: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if strings.Contains(st.Detail, "relaunch") {
		t.Errorf("no warning expected when the ports match, got: %q", st.Detail)
	}
}

// writeTestProfile drops a profile file carrying via_local and the given
// daemon port into the fake config dir.
func writeTestProfile(t *testing.T, configDir string, port int) {
	t.Helper()
	dir := filepath.Join(configDir, "proxy-helper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"via_local":true,"local_port":%d}`, port)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestSessionEnvDryRunTouchesNothing enforces the project rule that every
// side effect goes through the Executor: a target that shelled out directly
// would silently break --dry-run, which is the one thing a user leans on
// before letting the tool rewrite their machine.
func TestSessionEnvDryRunTouchesNothing(t *testing.T) {
	dir := withFakeConfigDir(t)
	log := fakeSystemctl(t, "")
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf}

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := NewSessionEnvTarget().Set(ex, cfg); err != nil {
		t.Fatalf("dry-run Set: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "environment.d", sessionEnvFileName)); !os.IsNotExist(err) {
		t.Errorf("a dry-run must not write the drop-in (err=%v)", err)
	}
	if calls := systemctlCalls(t, log); calls != "" {
		t.Errorf("a dry-run must not touch the live session, but ran:\n%s", calls)
	}
	if !strings.Contains(buf.String(), "would write") {
		t.Errorf("expected a dry-run preview of the file, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "would run: systemctl --user set-environment") {
		t.Errorf("expected a dry-run preview of the session update, got: %q", buf.String())
	}
}

// TestSessionEnvIsRegistered guards the wiring: a target that exists but is
// not in AllTargets is unreachable from --targets and invisible in status.
func TestSessionEnvIsRegistered(t *testing.T) {
	selected, err := ByNames([]string{"session-env"})
	if err != nil {
		t.Fatalf("session-env must be resolvable by name: %v", err)
	}
	if len(selected) != 1 || selected[0].Name() != "session-env" {
		t.Fatalf("ByNames returned %v", selected)
	}
}
