package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFirefox builds a profile tree and points the target at it.
func fakeFirefox(t *testing.T, profiles ...string) string {
	t.Helper()

	base := t.TempDir()
	for _, name := range profiles {
		if err := os.MkdirAll(filepath.Join(base, "Profiles", name), 0o700); err != nil {
			t.Fatalf("creating profile %s: %v", name, err)
		}
	}
	original := firefoxBaseDir
	firefoxBaseDir = func() (string, error) { return base, nil }
	t.Cleanup(func() { firefoxBaseDir = original })
	return base
}

func userJS(t *testing.T, base, profile string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(base, "Profiles", profile, "user.js"))
	if err != nil {
		t.Fatalf("reading user.js of %s: %v", profile, err)
	}
	return string(content)
}

// The whole point of the target: tell Firefox to stop ignoring the system
// settings the other targets write.
func TestFirefoxSetPointsAtTheSystemProxy(t *testing.T) {
	base := fakeFirefox(t, "default-release")

	if err := NewFirefoxTarget().Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if got := userJS(t, base, "default-release"); !strings.Contains(got, `user_pref("network.proxy.type", 5)`) {
		t.Errorf("user.js = %q, want it to select the system proxy", got)
	}
}

// A person with two profiles uses both; the one left out is the one that
// breaks.
func TestFirefoxSetConfiguresEveryProfile(t *testing.T) {
	base := fakeFirefox(t, "default-release", "trabalho")

	if err := NewFirefoxTarget().Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	for _, profile := range []string{"default-release", "trabalho"} {
		if got := userJS(t, base, profile); !strings.Contains(got, "network.proxy.type") {
			t.Errorf("profile %s was not configured", profile)
		}
	}
}

// The markers must be JavaScript comments: Firefox answers a syntax error
// in user.js by ignoring the entire file, so a "#" marker would silently
// discard the setting we just wrote.
func TestFirefoxSetWritesMarkersJavaScriptAccepts(t *testing.T) {
	base := fakeFirefox(t, "default-release")

	if err := NewFirefoxTarget().Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got := userJS(t, base, "default-release")
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			t.Errorf("user.js contains a line JavaScript cannot parse: %q", line)
		}
	}
}

// Someone else's settings in user.js are not ours to destroy.
func TestFirefoxPreservesSettingsItDidNotWrite(t *testing.T) {
	base := fakeFirefox(t, "default-release")
	path := filepath.Join(base, "Profiles", "default-release", "user.js")
	theirs := `user_pref("browser.startup.homepage", "https://intranet");`
	if err := os.WriteFile(path, []byte(theirs+"\n"), 0o600); err != nil {
		t.Fatalf("seeding user.js: %v", err)
	}

	target := NewFirefoxTarget()
	if err := target.Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := userJS(t, base, "default-release"); !strings.Contains(got, theirs) {
		t.Fatalf("Set destroyed an existing setting:\n%s", got)
	}

	if err := target.Unset(&Executor{}); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	got := userJS(t, base, "default-release")
	if !strings.Contains(got, theirs) {
		t.Errorf("Unset destroyed an existing setting:\n%s", got)
	}
	if strings.Contains(got, "network.proxy.type") {
		t.Errorf("Unset left our block behind:\n%s", got)
	}
}

// A user.js we created and then emptied is clutter in someone's profile.
func TestFirefoxUnsetRemovesAFileItCreated(t *testing.T) {
	base := fakeFirefox(t, "default-release")
	target := NewFirefoxTarget()
	if err := target.Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := target.Unset(&Executor{}); err != nil {
		t.Fatalf("Unset: %v", err)
	}

	if _, err := os.Stat(filepath.Join(base, "Profiles", "default-release", "user.js")); !os.IsNotExist(err) {
		t.Error("an emptied user.js was left behind")
	}
}

// A machine without Firefox must skip the target, never fail on it.
func TestFirefoxIsUnavailableWithoutProfiles(t *testing.T) {
	original := firefoxBaseDir
	firefoxBaseDir = func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { firefoxBaseDir = original })

	if NewFirefoxTarget().Available() {
		t.Error("reported available with no Profiles directory")
	}
}

// Half-configured is worth saying out loud: one profile still talks to the
// proxy directly.
func TestFirefoxStatusDistinguishesPartialFromComplete(t *testing.T) {
	if got := firefoxStatusDetail(2, 2); strings.Contains(got, " of ") {
		t.Errorf("detail = %q, want no count when every profile is configured", got)
	}
	if got := firefoxStatusDetail(1, 2); !strings.Contains(got, "1 of 2") {
		t.Errorf("detail = %q, want it to name how many profiles are configured", got)
	}
}

// The case behind the question: a profile the IT department pointed at the
// corporate proxy by hand. Turning our proxy off must give that back —
// Firefox never reverts a preference on its own, so deleting our line would
// leave the machine on "use the system proxy" forever, with no Firefox
// proxy on a network that requires one.
func TestFirefoxUnsetRestoresTheProxyTheProfileHadBefore(t *testing.T) {
	base := fakeFirefox(t, "default-release")
	profile := filepath.Join(base, "Profiles", "default-release")
	// 1 is Firefox's "manual proxy configuration".
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"),
		[]byte(`user_pref("network.proxy.type", 1);`+"\n"), 0o600); err != nil {
		t.Fatalf("seeding prefs.js: %v", err)
	}

	target := NewFirefoxTarget()
	if err := target.Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := target.Unset(&Executor{}); err != nil {
		t.Fatalf("Unset: %v", err)
	}

	got := userJS(t, base, "default-release")
	if !strings.Contains(got, `user_pref("network.proxy.type", 1)`) {
		t.Errorf("user.js = %q, want the profile's original manual proxy restored", got)
	}
}

// A profile that was already on "use the system settings" has nothing to
// restore, and must be left clean rather than carrying a block forever.
func TestFirefoxUnsetLeavesNothingBehindWhenThereWasNothingToRestore(t *testing.T) {
	base := fakeFirefox(t, "default-release")
	profile := filepath.Join(base, "Profiles", "default-release")
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"),
		[]byte(`user_pref("network.proxy.type", 5);`+"\n"), 0o600); err != nil {
		t.Fatalf("seeding prefs.js: %v", err)
	}

	target := NewFirefoxTarget()
	if err := target.Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := target.Unset(&Executor{}); err != nil {
		t.Fatalf("Unset: %v", err)
	}

	if _, err := os.Stat(filepath.Join(profile, "user.js")); !os.IsNotExist(err) {
		t.Error("a block was left behind with nothing to restore")
	}
}

// Turning the proxy on twice must not overwrite the recorded original with
// our own value — that would lose it just as surely as never recording it.
func TestFirefoxSetTwiceKeepsTheOriginalValue(t *testing.T) {
	base := fakeFirefox(t, "default-release")
	profile := filepath.Join(base, "Profiles", "default-release")
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"),
		[]byte(`user_pref("network.proxy.type", 1);`+"\n"), 0o600); err != nil {
		t.Fatalf("seeding prefs.js: %v", err)
	}

	target := NewFirefoxTarget()
	for i := 0; i < 2; i++ {
		if err := target.Set(&Executor{}, Config{Host: "10.0.0.1", Port: "3128"}); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}

	if got := userJS(t, base, "default-release"); !strings.Contains(got, previousMarker+"1") {
		t.Errorf("user.js = %q, want the original value still recorded as 1", got)
	}
}
