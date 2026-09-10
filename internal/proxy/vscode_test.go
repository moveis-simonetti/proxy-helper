package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeVscodeConfig points the target at a throwaway XDG_CONFIG_HOME and
// empties PATH, so nothing on the developer's machine — a real settings.json
// or a real `code` binary — can decide the outcome of a test.
func withFakeVscodeConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("PATH", "")
	return dir
}

// fakeEditorBinary puts an executable named cmd on PATH, standing in for an
// installed editor whose settings.json does not exist yet.
func fakeEditorBinary(t *testing.T, cmd string) {
	t.Helper()
	bin := t.TempDir()
	exe := filepath.Join(bin, cmd)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

// userDir creates <config>/<product>/User, which the editor creates on first
// run — before the user has changed any setting, so before settings.json
// exists.
func userDir(t *testing.T, configHome, product string) string {
	t.Helper()
	dir := filepath.Join(configHome, product, "User")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The reported bug: VS Code writes settings.json only once a setting is
// changed, so a fresh install has User/ and no file. Treating the missing
// file as "no editor" told the user their editor did not exist.
func TestVscodeStatusFindsEditorWithoutSettingsFile(t *testing.T) {
	home := withFakeVscodeConfig(t)
	userDir(t, home, "Code")

	st, err := (&vscodeTarget{}).Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Detail == "no VS Code-family editor found" {
		t.Errorf("Detail = %q, want the editor to be recognised", st.Detail)
	}
	if st.Detail != "not set" {
		t.Errorf("Detail = %q, want %q", st.Detail, "not set")
	}
}

// The same editor, detected by its CLI rather than its config directory —
// covers a first run that has not created User/ yet.
func TestVscodeStatusFindsEditorByBinary(t *testing.T) {
	withFakeVscodeConfig(t)
	fakeEditorBinary(t, "code")

	st, err := (&vscodeTarget{}).Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Detail != "not set" {
		t.Errorf("Detail = %q, want %q", st.Detail, "not set")
	}
}

// Set used to skip a missing settings.json, so applying the proxy silently
// did nothing — the failure the user actually felt, beyond the wrong label.
func TestVscodeSetCreatesMissingSettingsFile(t *testing.T) {
	home := withFakeVscodeConfig(t)
	dir := userDir(t, home, "Code")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := (&vscodeTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("settings.json was not created: %v", err)
	}
	if !strings.Contains(string(data), `"http.proxy": "http://127.0.0.1:8888"`) {
		t.Errorf("http.proxy missing from created file:\n%s", data)
	}
}

// The other direction must not regress: with no editor anywhere, the target
// still reports nothing and writes nothing. Creating ~/.config/Code on a
// machine that has no VS Code would be litter.
func TestVscodeAbsentEditorStaysAbsent(t *testing.T) {
	home := withFakeVscodeConfig(t)

	st, err := (&vscodeTarget{}).Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Detail != "no VS Code-family editor found" {
		t.Errorf("Detail = %q, want %q", st.Detail, "no VS Code-family editor found")
	}

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := (&vscodeTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("Set created %v in a machine with no editor", entries)
	}
}
