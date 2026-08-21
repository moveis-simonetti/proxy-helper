package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// fakeTarget stands in for a real target so tests can see exactly which
// Config would be written, without touching the developer's ~/.gitconfig,
// ~/.npmrc or /etc/apt.
type fakeTarget struct {
	name    string
	setCfgs []proxy.Config
	unsets  int
	// setErr makes this target fail the way a real one does when the tool's
	// own config file is unparsable or a sudo prompt is declined.
	setErr error
}

func (f *fakeTarget) Name() string       { return f.name }
func (f *fakeTarget) RequiresRoot() bool { return false }
func (f *fakeTarget) Available() bool    { return true }

func (f *fakeTarget) Set(ex *proxy.Executor, cfg proxy.Config) error {
	f.setCfgs = append(f.setCfgs, cfg)
	return f.setErr
}

func (f *fakeTarget) Unset(ex *proxy.Executor) error {
	f.unsets++
	return nil
}

func (f *fakeTarget) Status(bool) (proxy.Status, error) {
	return proxy.Status{Name: f.name, Available: true}, nil
}

// harness isolates one command run: a throwaway config dir, fake targets in
// place of the real ones, and a daemon that is "running" without systemd.
type harness struct {
	targets []*fakeTarget
	path    string
	reloads int
}

func newHarness(t *testing.T, configJSON string) *harness {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "proxy-helper", "config.json")
	if configJSON != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(configJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	h := &harness{
		targets: []*fakeTarget{{name: "git"}, {name: "apt"}},
		path:    path,
	}

	origTargets, origActive, origReload := resolveTargets, daemonActive, reloadDaemon
	resolveTargets = func([]string) ([]proxy.Target, error) {
		out := make([]proxy.Target, len(h.targets))
		for i, ft := range h.targets {
			out[i] = ft
		}
		return out, nil
	}
	daemonActive = func() bool { return true }
	reloadDaemon = func(*proxy.Executor) error { h.reloads++; return nil }
	t.Cleanup(func() {
		resolveTargets, daemonActive, reloadDaemon = origTargets, origActive, origReload
	})

	return h
}

// sets returns every Config that reached any target.
func (h *harness) sets() []proxy.Config {
	var out []proxy.Config
	for _, ft := range h.targets {
		out = append(out, ft.setCfgs...)
	}
	return out
}

func (h *harness) unsets() int {
	n := 0
	for _, ft := range h.targets {
		n += ft.unsets
	}
	return n
}

func (h *harness) profiles(t *testing.T) *proxy.ProfileFile {
	t.Helper()
	data, err := os.ReadFile(h.path)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	var pf proxy.ProfileFile
	if err := json.Unmarshal(data, &pf); err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	return &pf
}

// resetProfileFlags puts the shared cobra flag variables back to their
// defaults, since they are package globals shared between tests.
func resetProfileFlags(t *testing.T) {
	t.Helper()
	profileEnableTargets = []string{"all"}
	profileEnableDryRun = false
	profileEnableViaLocal = false
	profileDisableTargets = []string{"all"}
	profileDisableDryRun = false
	t.Cleanup(func() {
		profileEnableViaLocal = false
		profileEnableDryRun = false
	})
}

const workProfileJSON = `{"active_profile":"","profiles":{
	"work":{"scheme":"http","host":"proxy.corp","port":"8080","user":"alice","pass":"s3cr3t"},
	"home":{"scheme":"http","host":"home.proxy","port":"3128"}}}`

// TestProfileEnableViaLocalKeepsCredentialOutOfTargets is the regression
// test for the leak: with the plumbing in play, targets must receive the
// loopback address and nothing else. The real host, username and password
// belong to config.json only.
func TestProfileEnableViaLocalKeepsCredentialOutOfTargets(t *testing.T) {
	h := newHarness(t, workProfileJSON)
	resetProfileFlags(t)
	profileEnableViaLocal = true

	if err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"work"}); err != nil {
		t.Fatalf("profile enable --via-local: %v", err)
	}

	got := h.sets()
	if len(got) != len(h.targets) {
		t.Fatalf("got %d target writes, want %d", len(got), len(h.targets))
	}
	for _, cfg := range got {
		if cfg.Host != "127.0.0.1" {
			t.Errorf("target host = %q, want 127.0.0.1", cfg.Host)
		}
		if cfg.Port != "8888" {
			t.Errorf("target port = %q, want 8888", cfg.Port)
		}
		if cfg.Username != "" || cfg.Password != "" {
			t.Errorf("credential reached a target: user=%q pass=%q", cfg.Username, cfg.Password)
		}
		if rendered, err := cfg.URL(); err != nil {
			t.Fatal(err)
		} else if strings.Contains(rendered, "s3cr3t") || strings.Contains(rendered, "proxy.corp") {
			t.Errorf("target URL leaks the upstream: %q", rendered)
		}
	}
}

// TestProfileEnableViaLocalActivatesTheNamedProfile guards against the
// staleness bug: enabling a named profile must point active_profile at that
// name, not at a frozen copy in the reserved slot.
func TestProfileEnableViaLocalActivatesTheNamedProfile(t *testing.T) {
	h := newHarness(t, workProfileJSON)
	resetProfileFlags(t)
	profileEnableViaLocal = true

	if err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"work"}); err != nil {
		t.Fatalf("profile enable --via-local: %v", err)
	}

	pf := h.profiles(t)
	if pf.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work", pf.ActiveProfile)
	}
	if _, ok := pf.Profiles[proxy.CurrentProfileName]; ok {
		t.Error("enabling a named profile must not write the reserved slot")
	}
	if !pf.ViaLocal {
		t.Error("ViaLocal must be recorded")
	}
}

// TestProfileEnableWithPlumbingTouchesNoTarget is the core of the second
// axis: once the targets point at the daemon, switching profiles is pure
// state. No target may be written, with or without sudo.
func TestProfileEnableWithPlumbingTouchesNoTarget(t *testing.T) {
	h := newHarness(t, `{"active_profile":"work","via_local":true,"profiles":{
		"work":{"scheme":"http","host":"proxy.corp","port":"8080","user":"alice","pass":"s3cr3t"},
		"home":{"scheme":"http","host":"home.proxy","port":"3128"}}}`)
	resetProfileFlags(t)

	if err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"home"}); err != nil {
		t.Fatalf("profile enable: %v", err)
	}

	if got := h.sets(); len(got) != 0 {
		t.Fatalf("profile enable wrote %d target(s) while the plumbing was up: %+v", len(got), got)
	}
	pf := h.profiles(t)
	if pf.ActiveProfile != "home" {
		t.Errorf("ActiveProfile = %q, want home", pf.ActiveProfile)
	}
	if !pf.ViaLocal {
		t.Error("switching profiles must not tear down the plumbing")
	}
	if h.reloads == 0 {
		t.Error("the daemon must be told to reload")
	}
}

// TestProfileEnableWithoutPlumbingStillAppliesTheRealConfig keeps the legacy
// flow intact for anyone not running the daemon.
func TestProfileEnableWithoutPlumbingStillAppliesTheRealConfig(t *testing.T) {
	h := newHarness(t, workProfileJSON)
	resetProfileFlags(t)

	if err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"work"}); err != nil {
		t.Fatalf("profile enable: %v", err)
	}

	got := h.sets()
	if len(got) == 0 {
		t.Fatal("without the plumbing, targets must receive the real config")
	}
	if got[0].Host != "proxy.corp" || got[0].Password != "s3cr3t" {
		t.Errorf("target got %+v, want the real upstream", got[0])
	}
	if pf := h.profiles(t); pf.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work", pf.ActiveProfile)
	}
}

// TestViaLocalHonoursTheConfiguredPort proves the daemon port is propagated
// rather than hard-coded: a config installed on 9999 must send the targets
// to 9999.
func TestViaLocalHonoursTheConfiguredPort(t *testing.T) {
	h := newHarness(t, `{"local_port":9999,"profiles":{
		"work":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)
	resetProfileFlags(t)
	profileEnableViaLocal = true

	if err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"work"}); err != nil {
		t.Fatalf("profile enable --via-local: %v", err)
	}

	for _, cfg := range h.sets() {
		if cfg.Host != "127.0.0.1" || cfg.Port != "9999" {
			t.Errorf("target got %s:%s, want 127.0.0.1:9999", cfg.Host, cfg.Port)
		}
	}
}

// TestSetWithoutViaLocalClearsThePlumbingFlag: after writing the real
// upstream into the targets, nothing points at the loopback any more, so
// "proxy status" must stop warning about the daemon.
func TestSetWithoutViaLocalClearsThePlumbingFlag(t *testing.T) {
	h := newHarness(t, `{"via_local":true,"active_profile":"_current","profiles":{
		"_current":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	cfg := proxy.Config{Scheme: "http", Host: "other.corp", Port: "3128"}
	if err := applyConfig(cfg, []string{"all"}, false, false); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}

	if pf := h.profiles(t); pf.ViaLocal {
		t.Error("a plain \"proxy set\" must clear via_local")
	}
}

// TestPartialUnsetKeepsThePlumbingFlag: clearing one target leaves the rest
// pointing at the loopback, so the flag must survive; a full unset clears it.
func TestPartialUnsetKeepsThePlumbingFlag(t *testing.T) {
	const cfgJSON = `{"via_local":true,"profiles":{}}`

	h := newHarness(t, cfgJSON)
	if err := clearTargets([]string{"git"}, false); err != nil {
		t.Fatalf("clearTargets(git): %v", err)
	}
	if pf := h.profiles(t); !pf.ViaLocal {
		t.Error("a partial unset must not claim the plumbing is gone")
	}

	h2 := newHarness(t, cfgJSON)
	if err := clearTargets([]string{"all"}, false); err != nil {
		t.Fatalf("clearTargets(all): %v", err)
	}
	if pf := h2.profiles(t); pf.ViaLocal {
		t.Error("a full unset must clear via_local")
	}
	if h2.unsets() == 0 {
		t.Error("a full unset must actually clear the targets")
	}
}

// TestProfileDisableRemembersTheProfile: "proxy on" must work after a
// disable exactly as it does after an "proxy off".
func TestProfileDisableRemembersTheProfile(t *testing.T) {
	h := newHarness(t, `{"active_profile":"work","profiles":{
		"work":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)
	resetProfileFlags(t)

	if err := proxyProfileDisableCmd.RunE(proxyProfileDisableCmd, nil); err != nil {
		t.Fatalf("profile disable: %v", err)
	}

	pf := h.profiles(t)
	if pf.ActiveProfile != "" {
		t.Errorf("ActiveProfile = %q, want empty", pf.ActiveProfile)
	}
	if pf.LastProfile != "work" {
		t.Errorf("LastProfile = %q, want work so that \"proxy on\" works", pf.LastProfile)
	}
	if err := pf.On(""); err != nil {
		t.Errorf("On after disable: %v", err)
	}
}

func TestReservedProfileIsHiddenAndProtected(t *testing.T) {
	pf := &proxy.ProfileFile{Profiles: map[string]proxy.Config{
		proxy.CurrentProfileName: {Host: "proxy.corp"},
		"work":                   {Host: "proxy.corp"},
		"home":                   {Host: "home.proxy"},
	}}

	got := visibleProfileNames(pf)
	want := []string{"home", "work"}
	if len(got) != len(want) {
		t.Fatalf("visible profiles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("visible profiles = %v, want %v", got, want)
		}
	}

	for _, verb := range []string{"added", "edited", "removed"} {
		if err := refuseReserved(proxy.CurrentProfileName, verb); err == nil {
			t.Errorf("%q must be refused for %s", proxy.CurrentProfileName, verb)
		}
	}
	if err := refuseReserved("work", "edited"); err != nil {
		t.Errorf("a normal profile must not be refused: %v", err)
	}
}

// TestProfileEnableActivatesEvenWhenATargetFails locks the partial-state bug
// this tool exists to prevent. A target failing for its own reasons (here:
// an unparsable settings.json, which is what vscode does when the file holds
// JSON comments) must not cost the user the activation: the targets that did
// succeed now carry the proxy, so a config claiming no active profile leaves
// the machine inconsistent and the daemon routing direct.
func TestProfileEnableActivatesEvenWhenATargetFails(t *testing.T) {
	h := newHarness(t, workProfileJSON)
	resetProfileFlags(t)
	h.targets[1].setErr = errors.New("parsing settings.json: invalid character '/'")

	err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"work"})
	if err == nil {
		t.Fatal("the failing target must still be reported to the user")
	}
	if !strings.Contains(err.Error(), h.targets[1].name) {
		t.Errorf("error %q should name the failing target", err)
	}

	if pf := h.profiles(t); pf.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work: a failing target must not undo the activation", pf.ActiveProfile)
	}
}

// TestWarnsWhenThePasswordCannotReachTargets covers the configuration that
// silently cannot work: password_file is resolved only by the daemon, so
// writing the profile straight into each tool hands them a username with no
// password. The user must be told rather than left debugging 407s.
func TestWarnsWhenThePasswordCannotReachTargets(t *testing.T) {
	h := newHarness(t, `{"profiles":{"work":{"scheme":"http","host":"proxy.corp","port":"8080",
		"user":"alice","password_file":"/tmp/does-not-matter"}}}`)
	resetProfileFlags(t)

	out := captureStdout(t, func() {
		if err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"work"}); err != nil {
			t.Fatalf("profile enable: %v", err)
		}
	})

	if !strings.Contains(out, "password_file") {
		t.Errorf("expected a warning naming password_file, got:\n%s", out)
	}
	if !strings.Contains(out, "--via-local") {
		t.Errorf("the warning should point at the fix, got:\n%s", out)
	}
	// The warning must not be a substitute for doing the work.
	if len(h.sets()) == 0 {
		t.Error("targets should still be configured after the warning")
	}
}

// captureStdout collects what a command prints, so tests can assert on the
// guidance the user actually sees.
func captureStdout(t *testing.T, fn func()) string {
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

// TestProfileRemoveDropsDanglingReferences covers the residue left behind by
// deleting a profile: active_profile and last_profile must stop naming it,
// or "proxy on" later fails pointing at a name that no longer exists in
// "proxy profile list".
func TestProfileRemoveDropsDanglingReferences(t *testing.T) {
	h := newHarness(t, `{"active_profile":"work","last_profile":"work","profiles":{
		"work":{"scheme":"http","host":"proxy.corp","port":"8080"},
		"home":{"scheme":"http","host":"home.proxy","port":"3128"}}}`)
	resetProfileFlags(t)

	if err := proxyProfileRemoveCmd.RunE(proxyProfileRemoveCmd, []string{"work"}); err != nil {
		t.Fatalf("profile remove: %v", err)
	}

	pf := h.profiles(t)
	if pf.ActiveProfile != "" {
		t.Errorf("ActiveProfile = %q, want empty", pf.ActiveProfile)
	}
	if pf.LastProfile != "" {
		t.Errorf("LastProfile = %q, want empty: it names a profile that no longer exists", pf.LastProfile)
	}
	if _, ok := pf.Profiles["home"]; !ok {
		t.Error("removing one profile must not disturb the others")
	}
	if h.reloads == 0 {
		t.Error("removing the active profile must tell the daemon, which is still serving it")
	}
}

// TestProfileRemoveKeepsUnrelatedLastProfile is the other half: removing a
// profile that is not the remembered one must leave that memory alone.
func TestProfileRemoveKeepsUnrelatedLastProfile(t *testing.T) {
	h := newHarness(t, `{"active_profile":"","last_profile":"home","profiles":{
		"work":{"scheme":"http","host":"proxy.corp","port":"8080"},
		"home":{"scheme":"http","host":"home.proxy","port":"3128"}}}`)
	resetProfileFlags(t)

	if err := proxyProfileRemoveCmd.RunE(proxyProfileRemoveCmd, []string{"work"}); err != nil {
		t.Fatalf("profile remove: %v", err)
	}

	if pf := h.profiles(t); pf.LastProfile != "home" {
		t.Errorf("LastProfile = %q, want home", pf.LastProfile)
	}
	if h.reloads != 0 {
		t.Error("removing an inactive profile changes nothing for the daemon")
	}
}

// TestDockerTargetsGetABridgeAddress covers the failure found in real use:
// a build container cannot reach the host's 127.0.0.1, so image pulls kept
// working (dockerd runs on the host) while every build step that touched the
// network died on a connection refused.
func TestDockerTargetsGetABridgeAddress(t *testing.T) {
	h := newHarness(t, `{"docker_bridge":true,"local_port":8888,"profiles":{
		"work":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)
	h.targets = []*fakeTarget{{name: "git"}, {name: "dockerd"}, {name: "docker-config"}}
	resetProfileFlags(t)
	profileEnableViaLocal = true

	bridge, err := serve.DockerBridgeAddr()
	if err != nil {
		t.Skipf("no docker bridge on this machine: %v", err)
	}

	if err := proxyProfileEnableCmd.RunE(proxyProfileEnableCmd, []string{"work"}); err != nil {
		t.Fatalf("profile enable --via-local: %v", err)
	}

	for _, ft := range h.targets {
		if len(ft.setCfgs) == 0 {
			t.Fatalf("%s received no config", ft.name)
		}
		got := ft.setCfgs[0].Host
		want := "127.0.0.1"
		if serve.IsDockerTarget(ft.name) {
			want = bridge
		}
		if got != want {
			t.Errorf("%s got host %q, want %q", ft.name, got, want)
		}
		if ft.setCfgs[0].Port != "8888" {
			t.Errorf("%s got port %q, want 8888", ft.name, ft.setCfgs[0].Port)
		}
	}
}

// TestPartialSetKeepsTheViaLocalFlag guards the state lie: reconfiguring only
// some targets must not clear the flag, because the ones left alone still
// depend on the daemon and "proxy status" must keep warning about it.
func TestPartialSetKeepsTheViaLocalFlag(t *testing.T) {
	h := newHarness(t, `{"via_local":true,"active_profile":"_current","profiles":{
		"_current":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)
	resetProfileFlags(t)

	cfg := proxy.Config{Scheme: "http", Host: "192.168.0.9", Port: "3128"}
	if err := applyConfig(cfg, []string{"dockerd", "docker-config"}, false, false); err != nil {
		t.Fatalf("applyConfig: %v", err)
	}
	if pf := h.profiles(t); !pf.ViaLocal {
		t.Error("a partial set cleared via_local while other targets still point at the daemon")
	}

	if err := applyConfig(cfg, []string{"all"}, false, false); err != nil {
		t.Fatalf("applyConfig all: %v", err)
	}
	if pf := h.profiles(t); pf.ViaLocal {
		t.Error("a full set must clear via_local: no target points at the daemon any more")
	}
}

// TestNextStepHintIsRunnable guards against printing advice that is not a
// working command: "proxy set --via-local" alone fails with "--host is
// required", which is exactly the dead end a new user hits right after
// installing the service.
func TestNextStepHintIsRunnable(t *testing.T) {
	oneProfile := &proxy.ProfileFile{Profiles: map[string]proxy.Config{
		"trabalho": {Host: "proxy.corp", Port: "3128"},
	}}
	got := nextStepHint(oneProfile)
	if !strings.Contains(got, "--profile trabalho") {
		t.Errorf("with a single profile the hint should name it, got: %s", got)
	}

	two := &proxy.ProfileFile{Profiles: map[string]proxy.Config{
		"casa": {Host: "a"}, "trabalho": {Host: "b"},
	}}
	got = nextStepHint(two)
	if !strings.Contains(got, "casa") || !strings.Contains(got, "trabalho") {
		t.Errorf("with several profiles the hint should list them, got: %s", got)
	}

	empty := &proxy.ProfileFile{Profiles: map[string]proxy.Config{}}
	got = nextStepHint(empty)
	if !strings.Contains(got, "profile add") {
		t.Errorf("with no profiles the hint should say how to create one, got: %s", got)
	}

	plumbed := &proxy.ProfileFile{ViaLocal: true, Profiles: map[string]proxy.Config{"a": {Host: "x"}}}
	if got = nextStepHint(plumbed); !strings.Contains(got, "already") {
		t.Errorf("with the plumbing already up the hint should say so, got: %s", got)
	}

	// The reserved slot is an implementation detail and must never be
	// suggested as something to type.
	reserved := &proxy.ProfileFile{Profiles: map[string]proxy.Config{
		proxy.CurrentProfileName: {Host: "x"},
	}}
	if got = nextStepHint(reserved); strings.Contains(got, proxy.CurrentProfileName) {
		t.Errorf("the reserved profile leaked into user-facing advice: %s", got)
	}
}
