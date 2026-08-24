package proxy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGsettings drops an executable named "gsettings" on PATH (as the
// first entry) that accepts any "set" call (exit 0, mirroring the real
// dconf-commit-failed bug: gsettings exits 0 even when the write did not
// take effect) and answers every "get" call with wantGet, regardless of
// which schema/key was asked. It lets TestGnomeSetFailsWhenWriteDoesNotTakeEffect
// exercise the read-back verification deterministically, without a real
// desktop session.
func fakeGsettings(t *testing.T, wantGet string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  set) exit 0 ;;\n" +
		"  get) printf %s " + shellQuote(wantGet) + " ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gsettings"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fakeGsettingsEcho drops an executable "gsettings" that behaves like a
// (very small) real dconf backend: "set schema key value" remembers value
// per schema+key, and "get schema key" returns whatever was last set for
// that exact schema+key (or initial, before anything was set). Unlike
// fakeGsettings (which answers every get identically, for testing the
// read-back-mismatch path), this lets a test assert on the final state of
// several different keys after a sequence of writes — e.g. that clearing
// authentication actually clears it, without the mode write's read-back
// verification tripping over an unrelated key.
func fakeGsettingsEcho(t *testing.T, initial string) {
	t.Helper()
	dir := t.TempDir()
	store := t.TempDir()
	script := "#!/bin/sh\n" +
		"key=\"$2.$3\"\n" +
		"file=\"" + store + "/$(echo \"$key\" | tr -c 'A-Za-z0-9._-' '_')\"\n" +
		"case \"$1\" in\n" +
		"  set) printf %s \"$4\" > \"$file\"; exit 0 ;;\n" +
		"  get)\n" +
		"    if [ -f \"$file\" ]; then cat \"$file\"; else printf %s " + shellQuote(initial) + "; fi\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(dir, "gsettings"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGnomeSetFailsWhenWriteDoesNotTakeEffect is the regression test for
// the CLI reporting success on a "gsettings set" that never actually
// committed (e.g. no session D-Bus reachable): "gsettings set" exits 0
// regardless, so Set must read the value back and fail when it does not
// match what it just tried to write.
func TestGnomeSetFailsWhenWriteDoesNotTakeEffect(t *testing.T) {
	fakeGsettings(t, "'none'") // every read reports the pre-existing state
	ex := &Executor{Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	err := NewGnomeTarget().Set(ex, Config{Host: "proxy.local", Port: "8080"})
	if err == nil {
		t.Fatal("expected Set to fail when the mode read-back does not match what was written")
	}
	if !strings.Contains(err.Error(), "did not take effect") {
		t.Errorf("expected an explanation of the read-back mismatch, got: %v", err)
	}
}

// TestGnomeSetDryRunSkipsVerification guards the dry-run path: an
// Executor{DryRun: true} never actually runs "gsettings set", so there is
// nothing to read back, and verification must not turn the dry-run into a
// false failure.
func TestGnomeSetDryRunSkipsVerification(t *testing.T) {
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf}

	if err := NewGnomeTarget().Set(ex, Config{Host: "proxy.local", Port: "8080"}); err != nil {
		t.Fatalf("dry-run Set should not fail, got: %v", err)
	}
	if !strings.Contains(buf.String(), "would run: gsettings set org.gnome.system.proxy mode manual") {
		t.Errorf("expected the usual dry-run preview, got: %q", buf.String())
	}
}

// TestGnomeSessionScoped pins gnome as session-scoped: a caller deciding
// whether to elevate must never route it into a privileged/pkexec call,
// regardless of RequiresRoot.
func TestGnomeSessionScoped(t *testing.T) {
	if !NewGnomeTarget().SessionScoped() {
		t.Error("gnome must report SessionScoped() == true")
	}
}

// TestGnomeUnsetSkipsPackageKitWorkaroundWithoutElevation covers the
// choice made for gnome's Unset when it runs in-process, unelevated (the
// GUI's EscalateNone executor, now that gnome is never routed through
// pkexec): the PackageKit cache workaround needs root it cannot get here,
// so it must be skipped with a warning rather than failing the whole
// Unset (mode=none, the actual proxy removal, already succeeded) or doing
// nothing silently.
func TestGnomeUnsetSkipsPackageKitWorkaroundWithoutElevation(t *testing.T) {
	if IsRoot() {
		t.Skip("running as root, nothing to test the refusal path with")
	}
	fakeGsettingsEcho(t, "'none'")
	var stderr bytes.Buffer
	ex := &Executor{Out: &bytes.Buffer{}, Stderr: &stderr, Escalation: EscalateNone}

	if err := NewGnomeTarget().Unset(ex); err != nil {
		t.Fatalf("Unset should not fail when only the optional PackageKit workaround is unreachable, got: %v", err)
	}
}

// TestGnomeSetClearsStaleAuthentication is the regression test for the
// defect where Set only ever added authentication (use-authentication,
// authentication-user/-password when cfg.Username != "") and never
// removed it: a config with no username (e.g. the local daemon's
// credential-free loopback URL) left an earlier profile's username and
// use-authentication=true sitting in dconf, so GNOME kept offering a dead
// username to a proxy that never asked for one.
func TestGnomeSetClearsStaleAuthentication(t *testing.T) {
	fakeGsettingsEcho(t, "''")
	ex := &Executor{Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}

	// First, a profile with credentials — mirrors an earlier Apply that
	// did carry a username.
	if err := NewGnomeTarget().Set(ex, Config{Host: "proxy.example", Port: "8080", Username: "alice", Password: "secret"}); err != nil {
		t.Fatalf("first Set (with credentials) failed: %v", err)
	}
	if got := gsettingsGetString("org.gnome.system.proxy.http", "authentication-user"); got != "alice" {
		t.Fatalf("sanity check: expected authentication-user 'alice' after first Set, got %q", got)
	}

	// Then, a config with no username — e.g. --via-local, whose URL never
	// carries credentials. The earlier username/use-authentication must
	// not survive this second Set.
	if err := NewGnomeTarget().Set(ex, Config{Host: "127.0.0.1", Port: "8888"}); err != nil {
		t.Fatalf("second Set (no credentials) failed: %v", err)
	}

	if got := gsettingsGetString("org.gnome.system.proxy.http", "use-authentication"); got != "false" {
		t.Errorf("use-authentication: got %q, want %q", got, "false")
	}
	if got := gsettingsGetString("org.gnome.system.proxy.http", "authentication-user"); got != "" {
		t.Errorf("authentication-user: got %q, want empty", got)
	}
	if got := gsettingsGetString("org.gnome.system.proxy.http", "authentication-password"); got != "" {
		t.Errorf("authentication-password: got %q, want empty", got)
	}
}

// TestGnomeUnsetClearsAuthentication covers Unset: it must not leave
// credentials behind either, even though mode=none already stops GNOME
// from using the proxy at all.
func TestGnomeUnsetClearsAuthentication(t *testing.T) {
	if IsRoot() {
		t.Skip("running as root, nothing to test the refusal path with")
	}
	fakeGsettingsEcho(t, "''")
	ex := &Executor{Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Escalation: EscalateNone}

	if err := NewGnomeTarget().Set(ex, Config{Host: "proxy.example", Port: "8080", Username: "alice", Password: "secret"}); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if err := NewGnomeTarget().Unset(ex); err != nil {
		t.Fatalf("Unset failed: %v", err)
	}

	if got := gsettingsGetString("org.gnome.system.proxy.http", "use-authentication"); got != "false" {
		t.Errorf("use-authentication: got %q, want %q", got, "false")
	}
	if got := gsettingsGetString("org.gnome.system.proxy.http", "authentication-user"); got != "" {
		t.Errorf("authentication-user: got %q, want empty", got)
	}
}
