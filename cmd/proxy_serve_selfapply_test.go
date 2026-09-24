package cmd

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"proxy-helper/internal/proxy"
)

func isolatedConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	return filepath.Join(dir, "proxy-helper", "config.json")
}

func writeIsolatedConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The whole point: a daemon whose targets do not point at it yet fixes that
// on its own, exactly once, without anyone running a command.
func TestSelfApplyUserTargetsSetsViaLocal(t *testing.T) {
	path := isolatedConfigDir(t)
	writeIsolatedConfig(t, path, `{"profiles":{}}`)

	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	selfApplyUserTargets(logger, pf)

	if !pf.ViaLocal {
		t.Error("ViaLocal on the in-memory pf was not set")
	}
	reread, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles (reread): %v", err)
	}
	if !reread.ViaLocal {
		t.Error("via_local was not persisted to disk")
	}
}

// Once applied, it must not run again on every restart — that would rewrite
// every unprivileged target's config on every reboot for no reason.
func TestSelfApplyUserTargetsIsANoOpWhenAlreadyApplied(t *testing.T) {
	path := isolatedConfigDir(t)
	writeIsolatedConfig(t, path, `{"via_local":true,"profiles":{}}`)

	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	selfApplyUserTargets(logger, pf)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("config.json was rewritten even though via_local was already true")
	}
}
