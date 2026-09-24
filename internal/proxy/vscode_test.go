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
	// HOME too: snap installs ignore XDG_CONFIG_HOME and keep their config
	// under ~/snap/<pkg>/current, so a test that only redirected the former
	// would read the developer's real home.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")
	return dir
}

// flatpakUserDir creates the sandboxed config directory a flatpak-installed
// editor uses: ~/.var/app/<app-id>/config/<product>/User.
func flatpakUserDir(t *testing.T, appID, product string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".var", "app", appID, "config", product, "User")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// flatpakExportsBin drops the wrapper flatpak creates at install time under
// $XDG_DATA_HOME/flatpak/exports/bin/<app-id> — the signal for an editor
// installed but never launched, so ~/.var/app/<id> does not exist yet.
func flatpakExportsBin(t *testing.T, appID string) {
	t.Helper()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	dir := filepath.Join(dataHome, "flatpak", "exports", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, appID), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// snapUserDir creates the confined config directory a snap-installed editor
// uses: ~/snap/<pkg>/current/.config/<product>/User.
func snapUserDir(t *testing.T, snapPkg, product string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), "snap", snapPkg, "current", ".config", product, "User")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
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

// A snap-installed VS Code keeps settings.json inside its confined home, so
// the three $XDG_CONFIG_HOME paths find nothing and the editor reads as
// absent — the second half of the same user report.
func TestVscodeStatusFindsSnapInstall(t *testing.T) {
	withFakeVscodeConfig(t)
	snapUserDir(t, "code", "Code")

	st, err := (&vscodeTarget{}).Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Detail != "not set" {
		t.Errorf("Detail = %q, want %q", st.Detail, "not set")
	}
}

func TestVscodeSetWritesInsideSnapHome(t *testing.T) {
	withFakeVscodeConfig(t)
	dir := snapUserDir(t, "code", "Code")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := (&vscodeTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("snap settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), `"http.proxy": "http://127.0.0.1:8888"`) {
		t.Errorf("http.proxy missing:\n%s", data)
	}
}

// Both packagings side by side is a real state — a leftover .deb next to a
// snap — and each has its own settings.json. Configuring only one leaves the
// editor the user actually launches unproxied.
func TestVscodeSetWritesBothPackagings(t *testing.T) {
	home := withFakeVscodeConfig(t)
	native := userDir(t, home, "Code")
	snap := snapUserDir(t, "code", "Code")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := (&vscodeTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for _, dir := range []string{native, snap} {
		if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
			t.Errorf("settings.json missing in %s: %v", dir, err)
		}
	}

	st, err := (&vscodeTarget{}).Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	// Distinct labels, or the status line reads as one editor listed twice.
	for _, want := range []string{"VS Code=", "VS Code (snap)="} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("Detail = %q, want it to mention %q", st.Detail, want)
		}
	}
}

// A snap CLI lives on /snap/bin. Detecting the editor by that binary and
// then writing to ~/.config would put the proxy in a file the confined
// editor cannot read, so the packaging has to steer the path.
func TestSnapBinaryPath(t *testing.T) {
	cases := map[string]bool{
		"/snap/bin/code":      true,
		"/usr/bin/code":       false,
		"/usr/local/bin/code": false,
		"":                    false,
	}
	for path, want := range cases {
		if got := isSnapBinary(path); got != want {
			t.Errorf("isSnapBinary(%q) = %v, want %v", path, got, want)
		}
	}
}

// The third location from the same report: a flatpak-confined VS Code keeps
// settings.json under ~/.var/app/<id>/config, invisible to both the native
// and the snap paths.
func TestVscodeStatusFindsFlatpakInstall(t *testing.T) {
	withFakeVscodeConfig(t)
	flatpakUserDir(t, "com.visualstudio.code", "Code")

	st, err := (&vscodeTarget{}).Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Detail != "not set" {
		t.Errorf("Detail = %q, want %q", st.Detail, "not set")
	}
}

func TestVscodeSetWritesInsideFlatpakHome(t *testing.T) {
	withFakeVscodeConfig(t)
	dir := flatpakUserDir(t, "com.visualstudio.code", "Code")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := (&vscodeTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("flatpak settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), `"http.proxy": "http://127.0.0.1:8888"`) {
		t.Errorf("http.proxy missing:\n%s", data)
	}
}

// Installed but never launched: no ~/.var/app/<id> yet, only the exports
// wrapper. Set must still create the settings.json in the flatpak location.
func TestVscodeSetFindsFlatpakByExportsBin(t *testing.T) {
	withFakeVscodeConfig(t)
	flatpakExportsBin(t, "com.visualstudio.code")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := (&vscodeTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	want := filepath.Join(os.Getenv("HOME"), ".var", "app", "com.visualstudio.code", "config", "Code", "User", "settings.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("settings.json not created at flatpak path: %v", err)
	}
}

// All three packagings of one editor, side by side. Each has its own
// settings.json and its own status label; configuring two and missing the
// third would leave whichever the user launches unproxied.
func TestVscodeSetWritesAllThreePackagings(t *testing.T) {
	home := withFakeVscodeConfig(t)
	native := userDir(t, home, "Code")
	snap := snapUserDir(t, "code", "Code")
	flat := flatpakUserDir(t, "com.visualstudio.code", "Code")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	if err := (&vscodeTarget{}).Set(&Executor{}, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for _, dir := range []string{native, snap, flat} {
		if _, err := os.Stat(filepath.Join(dir, "settings.json")); err != nil {
			t.Errorf("settings.json missing in %s: %v", dir, err)
		}
	}

	st, err := (&vscodeTarget{}).Status(&Executor{}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, want := range []string{"VS Code=", "VS Code (snap)=", "VS Code (flatpak)="} {
		if !strings.Contains(st.Detail, want) {
			t.Errorf("Detail = %q, want it to mention %q", st.Detail, want)
		}
	}
}
