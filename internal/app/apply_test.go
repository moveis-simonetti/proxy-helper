package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"proxy-helper/internal/proxy"
)

// fakeTarget records the Configs it receives instead of touching the
// developer's real ~/.gitconfig or /etc/apt.
type fakeTarget struct {
	name          string
	root          bool
	sessionScoped bool
	available     bool
	setCfgs       []proxy.Config
	unsets        int
	setErr        error
	unsetErr      error
	statusErr     error
	statusCalls   int
	// statusDetail, when set, is returned as Status.Detail — mirroring the
	// reason a real target like kde/lxd/snap gives for being unavailable.
	statusDetail string
	// trace, when set, records "set:<name>" from Set so a test can assert
	// ordering against something else appended to the same slice (e.g. a
	// Notify callback).
	trace *[]string
	// onSet, when set, runs at the very start of Set — before setErr is
	// returned — so a test can observe state that only matters at the exact
	// moment a target is configured (e.g. what active_profile reads on disk
	// right then), rather than after Set already returned.
	onSet func()
}

func (f *fakeTarget) Name() string        { return f.name }
func (f *fakeTarget) RequiresRoot() bool  { return f.root }
func (f *fakeTarget) SessionScoped() bool { return f.sessionScoped }
func (f *fakeTarget) Available() bool     { return f.available }

func (f *fakeTarget) Set(ex *proxy.Executor, cfg proxy.Config) error {
	if f.onSet != nil {
		f.onSet()
	}
	f.setCfgs = append(f.setCfgs, cfg)
	if f.trace != nil {
		*f.trace = append(*f.trace, "set:"+f.name)
	}
	return f.setErr
}

func (f *fakeTarget) Unset(ex *proxy.Executor) error {
	f.unsets++
	if f.trace != nil {
		*f.trace = append(*f.trace, "unset:"+f.name)
	}
	return f.unsetErr
}

func (f *fakeTarget) Status(ex *proxy.Executor, elevate bool) (proxy.Status, error) {
	f.statusCalls++
	if f.statusErr != nil {
		return proxy.Status{}, f.statusErr
	}
	return proxy.Status{Name: f.name, Available: f.available, Detail: f.statusDetail}, nil
}

func depsFor(targets ...*fakeTarget) Deps {
	return Deps{
		// ResolveTargets filters by name, treating "all" as a wildcard —
		// this matters for Elevate, which asks for only the targets that
		// need re-reading rather than the full set.
		ResolveTargets: func(names []string) ([]proxy.Target, error) {
			wantAll := len(names) == 1 && names[0] == "all"
			var out []proxy.Target
			for _, t := range targets {
				if wantAll {
					out = append(out, t)
					continue
				}
				for _, n := range names {
					if n == t.name {
						out = append(out, t)
						break
					}
				}
			}
			return out, nil
		},
		DaemonActive: func() bool { return true },
		ReloadDaemon: func(*proxy.Executor) error { return nil },
		BridgeAddr:   func() (string, error) { return "172.17.0.1", nil },
	}
}

// isolateConfig points proxy.ConfigFilePath at a throwaway directory.
func isolateConfig(t *testing.T, contents string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if contents == "" {
		return
	}
	path := filepath.Join(dir, "proxy-helper", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestApplyMergesTheGlobalNoProxy(t *testing.T) {
	isolateConfig(t, `{"global_no_proxy":["*.corp"],"profiles":{}}`)
	git := &fakeTarget{name: "git", available: true}

	rep, err := Apply(depsFor(git), &proxy.Executor{}, "", proxy.Config{
		Host: "proxy.corp", Port: "8080", NoProxy: []string{"*.lan"},
	}, []string{"git"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rep.Err() != nil {
		t.Fatalf("no target should have failed: %v", rep.Err())
	}

	if len(git.setCfgs) != 1 {
		t.Fatalf("expected one Set, got %d", len(git.setCfgs))
	}
	got := git.setCfgs[0].NoProxyString()
	if got != "*.corp,*.lan" {
		t.Errorf("expected the global list first, got %q", got)
	}
}

func TestApplySkipsUnavailableTargetsWithoutFailing(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	kde := &fakeTarget{name: "kde", available: false}

	rep, err := Apply(depsFor(kde), &proxy.Executor{}, "", proxy.Config{Host: "p", Port: "1"}, []string{"kde"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rep.Err() != nil {
		t.Fatalf("an unavailable target is not a failure: %v", rep.Err())
	}
	if len(rep.Results) != 1 || rep.Results[0].Outcome != OutcomeSkipped {
		t.Errorf("expected one skipped result, got %+v", rep.Results)
	}
	if len(kde.setCfgs) != 0 {
		t.Error("an unavailable target must not be configured")
	}
}

func TestApplyKeepsGoingAfterOneTargetFails(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	bad := &fakeTarget{name: "apt", available: true, setErr: os.ErrPermission}
	good := &fakeTarget{name: "git", available: true}

	rep, err := Apply(depsFor(bad, good), &proxy.Executor{}, "", proxy.Config{Host: "p", Port: "1"}, []string{"all"}, false)
	if err != nil {
		t.Fatalf("a per-target failure is not a global error: %v", err)
	}
	if len(good.setCfgs) != 1 {
		t.Error("the second target must still be configured")
	}
	if failed := rep.Failed(); len(failed) != 1 || failed[0] != "apt" {
		t.Errorf("expected apt to be the only failure, got %v", failed)
	}
}

func TestApplyWarnsWhenThePasswordCannotReachTargets(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	git := &fakeTarget{name: "git", available: true}

	rep, err := Apply(depsFor(git), &proxy.Executor{}, "", proxy.Config{
		Host: "proxy.corp", Port: "8080", Username: "alice", PasswordFile: "/tmp/pass",
	}, []string{"git"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var found bool
	for _, n := range rep.Notices {
		if n.Kind == NoticeUnreachablePassword && n.Args["source"] == "password_file" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unreachable-password notice, got %+v", rep.Notices)
	}
	if len(git.setCfgs) == 0 {
		t.Error("the warning must not replace doing the work")
	}
}

func TestClearRecordsClearedOutcomes(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	git := &fakeTarget{name: "git", available: true}

	rep, err := Clear(depsFor(git), &proxy.Executor{}, []string{"git"})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if git.unsets != 1 {
		t.Errorf("expected one Unset, got %d", git.unsets)
	}
	if rep.Results[0].Outcome != OutcomeCleared {
		t.Errorf("expected OutcomeCleared, got %v", rep.Results[0].Outcome)
	}
}

// TestApplyWarnsThatDockerdNeedsRestart guards the fix for a note that used
// to reach the user only by accident, via fmt.Println in dockerd.go's Set,
// captured on the elevated process's stdout. It must now be a typed Notice,
// scoped to dockerd alone: docker-config touches ~/.docker/config.json,
// which every docker command reads fresh, so it never needs a restart.
func TestApplyWarnsThatDockerdNeedsRestart(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	dockerd := &fakeTarget{name: "dockerd", available: true}
	dockerConfig := &fakeTarget{name: "docker-config", available: true}

	rep, err := Apply(depsFor(dockerd, dockerConfig), &proxy.Executor{}, "", proxy.Config{
		Host: "proxy.corp", Port: "8080",
	}, []string{"all"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var found *Notice
	for i := range rep.Notices {
		if rep.Notices[i].Kind == NoticeDockerNeedsRestart {
			found = &rep.Notices[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a NoticeDockerNeedsRestart, got %+v", rep.Notices)
	}
	if found.Target != "dockerd" {
		t.Errorf("expected the notice to name dockerd, got %q", found.Target)
	}
	if found.Args["op"] != "apply" {
		t.Errorf("expected Args[op]=apply, got %+v", found.Args)
	}
}

// TestClearWarnsThatDockerdNeedsRestart is TestApplyWarnsThatDockerdNeedsRestart's
// counterpart for Clear: removing the drop-in still needs a restart to take
// effect, but the wording differs (no mention of running containers being
// restarted by the change itself), so Clear must set Args["op"]="clear".
func TestClearWarnsThatDockerdNeedsRestart(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	dockerd := &fakeTarget{name: "dockerd", available: true}
	dockerConfig := &fakeTarget{name: "docker-config", available: true}

	rep, err := Clear(depsFor(dockerd, dockerConfig), &proxy.Executor{}, []string{"all"})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}

	var found *Notice
	for i := range rep.Notices {
		if rep.Notices[i].Kind == NoticeDockerNeedsRestart {
			found = &rep.Notices[i]
		}
	}
	if found == nil {
		t.Fatalf("expected a NoticeDockerNeedsRestart, got %+v", rep.Notices)
	}
	if found.Target != "dockerd" {
		t.Errorf("expected the notice to name dockerd, got %q", found.Target)
	}
	if found.Args["op"] != "clear" {
		t.Errorf("expected Args[op]=clear, got %+v", found.Args)
	}
}

// TestNoDockerRestartNoticeWhenOnlyDockerConfigApplied is the negative case:
// docker-config alone must never trigger the restart notice, since it does
// not write the systemd drop-in that a restart would pick up.
func TestNoDockerRestartNoticeWhenOnlyDockerConfigApplied(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	dockerConfig := &fakeTarget{name: "docker-config", available: true}

	rep, err := Apply(depsFor(dockerConfig), &proxy.Executor{}, "", proxy.Config{
		Host: "proxy.corp", Port: "8080",
	}, []string{"docker-config"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, n := range rep.Notices {
		if n.Kind == NoticeDockerNeedsRestart {
			t.Errorf("docker-config alone must not raise NoticeDockerNeedsRestart, got %+v", n)
		}
	}
}

// TestClearRecordsFailedOutcome closes the coverage gap left by fakeTarget's
// Unset never failing: it exercises Clear's OutcomeFailed path, which until
// now only setEach's equivalent path was tested.
func TestClearRecordsFailedOutcome(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	bad := &fakeTarget{name: "apt", available: true, unsetErr: os.ErrPermission}
	good := &fakeTarget{name: "git", available: true}

	rep, err := Clear(depsFor(bad, good), &proxy.Executor{}, []string{"all"})
	if err != nil {
		t.Fatalf("a per-target failure is not a global error: %v", err)
	}
	if good.unsets != 1 {
		t.Error("the other target must still be cleared")
	}
	if failed := rep.Failed(); len(failed) != 1 || failed[0] != "apt" {
		t.Errorf("expected apt to be the only failure, got %v", failed)
	}
	for _, res := range rep.Results {
		if res.Target == "apt" && res.Outcome != OutcomeFailed {
			t.Errorf("expected apt to be OutcomeFailed, got %v", res.Outcome)
		}
	}
}

// TestNotifyFiresBeforeSudoRequiringSet pins the ordering fix: the CLI
// relies on Notify delivering the sudo notice before Set is called, because
// Set is what triggers the sudo password prompt. If Notify fired after Set
// (or only at the end, via Report.Notices), the warning would arrive too
// late to be useful.
func TestNotifyFiresBeforeSudoRequiringSet(t *testing.T) {
	if proxy.IsRoot() {
		t.Skip("the sudo notice is only raised for a non-root process")
	}
	isolateConfig(t, `{"profiles":{}}`)
	var trace []string
	apt := &fakeTarget{name: "apt", available: true, root: true, trace: &trace}

	d := depsFor(apt)
	d.Notify = func(n Notice) {
		if n.Kind == NoticeNeedsSudo {
			trace = append(trace, "notify:"+n.Target)
		}
	}

	_, err := Apply(d, &proxy.Executor{}, "", proxy.Config{Host: "p", Port: "1"}, []string{"apt"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := []string{"notify:apt", "set:apt"}
	if len(trace) != len(want) {
		t.Fatalf("expected trace %v, got %v", want, trace)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("expected trace %v, got %v", want, trace)
		}
	}
}

// TestApplyViaLocalWrapsErrDaemonNotRunning locks in the sentinel error: a
// GUI must be able to detect a stopped daemon with errors.Is instead of
// matching the CLI's instruction text.
func TestApplyViaLocalWrapsErrDaemonNotRunning(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	d := depsFor()
	d.DaemonActive = func() bool { return false }

	_, err = ApplyViaLocal(d, &proxy.Executor{}, pf, proxy.Config{Host: "p", Port: "1"}, []string{"git"})
	if !errors.Is(err, ErrDaemonNotRunning) {
		t.Fatalf("expected errors.Is(err, ErrDaemonNotRunning), got %v", err)
	}
	const wantText = `the local proxy is not running; run "proxy serve install" first`
	if err.Error() != wantText {
		t.Errorf("error text changed, got %q, want %q", err.Error(), wantText)
	}
}

// TestSkippedResultCarriesTheReason locks in that a skipped target's Detail
// carries the reason it gave for being unavailable (e.g. kde saying
// "kwriteconfig not found"), so a GUI can show it without reimplementing
// each target's availability check.
func TestSkippedResultCarriesTheReason(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)
	kde := &fakeTarget{name: "kde", available: false, statusDetail: "kwriteconfig not found"}

	rep, err := Apply(depsFor(kde), &proxy.Executor{}, "", proxy.Config{Host: "p", Port: "1"}, []string{"kde"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rep.Results[0].Detail != "kwriteconfig not found" {
		t.Errorf("a skipped target must say why, got %q", rep.Results[0].Detail)
	}
}

// TestSetNeverWarnsAboutSudoForSessionScopedTarget covers the fix for the
// GUI showing "gnome precisa de sudo" on a run that never actually
// prompted: gnome.RequiresRoot() is true only because of its Unset-only
// PackageKit workaround, so a session-scoped target's Set must never raise
// NoticeNeedsSudo even when RequiresRoot() reports true.
func TestSetNeverWarnsAboutSudoForSessionScopedTarget(t *testing.T) {
	if proxy.IsRoot() {
		t.Skip("the sudo notice is only raised for a non-root process")
	}
	isolateConfig(t, `{"profiles":{}}`)
	gnome := &fakeTarget{name: "gnome", available: true, root: true, sessionScoped: true}

	var notices []Notice
	d := depsFor(gnome)
	d.Notify = func(n Notice) { notices = append(notices, n) }

	if _, err := Apply(d, &proxy.Executor{}, "", proxy.Config{Host: "p", Port: "1"}, []string{"gnome"}, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, n := range notices {
		if n.Kind == NoticeNeedsSudo {
			t.Errorf("Set on a session-scoped target must never raise NoticeNeedsSudo, got %+v", n)
		}
	}
}

// TestClearStillWarnsAboutSudoForSessionScopedTarget is the CLI-sensibility
// half of the same fix: unlike Set, gnome's Unset genuinely may need sudo
// for the PackageKit workaround, so "proxy unset --targets gnome" from a
// plain (non-EscalateNone) CLI invocation must keep the notice.
func TestClearStillWarnsAboutSudoForSessionScopedTarget(t *testing.T) {
	if proxy.IsRoot() {
		t.Skip("the sudo notice is only raised for a non-root process")
	}
	isolateConfig(t, `{"profiles":{}}`)
	gnome := &fakeTarget{name: "gnome", available: true, root: true, sessionScoped: true}

	var notices []Notice
	d := depsFor(gnome)
	d.Notify = func(n Notice) { notices = append(notices, n) }

	if _, err := Clear(d, &proxy.Executor{}, []string{"gnome"}); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	found := false
	for _, n := range notices {
		if n.Kind == NoticeNeedsSudo && n.Target == "gnome" {
			found = true
		}
	}
	if !found {
		t.Error("Unset on a session-scoped target whose workaround genuinely needs root must still raise NoticeNeedsSudo")
	}
}

// TestEscalateNoneNeverWarnsAboutSudo covers the GUI's in-process executor
// (Escalation: EscalateNone): it refuses to elevate outright (see
// Executor's doc comment), so no password prompt can ever appear — warning
// about one would be false regardless of RequiresRoot or SessionScoped.
func TestEscalateNoneNeverWarnsAboutSudo(t *testing.T) {
	if proxy.IsRoot() {
		t.Skip("the sudo notice is only raised for a non-root process")
	}
	isolateConfig(t, `{"profiles":{}}`)
	apt := &fakeTarget{name: "apt", available: true, root: true}

	var notices []Notice
	d := depsFor(apt)
	d.Notify = func(n Notice) { notices = append(notices, n) }

	ex := &proxy.Executor{Escalation: proxy.EscalateNone}
	if _, err := Apply(d, ex, "", proxy.Config{Host: "p", Port: "1"}, []string{"apt"}, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, n := range notices {
		if n.Kind == NoticeNeedsSudo {
			t.Errorf("EscalateNone must never raise NoticeNeedsSudo, got %+v", n)
		}
	}
}

// A named profile applied through the local daemon must stay active under
// its own name. It did not: Apply called SetCurrent unconditionally on the
// viaLocal path, so "proxy set --profile Trabalho --via-local" — and every
// Aplicar on the GUI's Status page, which applies the active profile's
// config — rewrote active_profile to the reserved "_current" slot. The name
// the user chose was lost, and the GUI's profile selector went blank because
// "_current" is deliberately hidden from the profiles list.
func TestApplyViaLocalKeepsTheProfileNameActive(t *testing.T) {
	isolateConfig(t, `{"active_profile":"Trabalho","via_local":true,"profiles":{"Trabalho":{"host":"p","port":"1"}}}`)

	git := &fakeTarget{name: "git"}
	d := depsFor(git)
	d.DaemonActive = func() bool { return true }

	if _, err := Apply(d, &proxy.Executor{}, "Trabalho", proxy.Config{Host: "p", Port: "1"}, []string{"git"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if pf.ActiveProfile != "Trabalho" {
		t.Errorf("active_profile = %q, want %q", pf.ActiveProfile, "Trabalho")
	}
	if _, ok := pf.Profiles[proxy.CurrentProfileName]; ok {
		t.Errorf("%q was written for a config that had a name", proxy.CurrentProfileName)
	}
}

// The reserved slot still earns its keep: "proxy set --host ... --via-local"
// really has no name, and the daemon only ever reads active_profile, so
// something must be active for it to route through.
func TestApplyViaLocalStillUsesTheReservedSlotForAnAdHocConfig(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	git := &fakeTarget{name: "git"}
	d := depsFor(git)
	d.DaemonActive = func() bool { return true }

	if _, err := Apply(d, &proxy.Executor{}, "", proxy.Config{Host: "adhoc", Port: "1"}, []string{"git"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if pf.ActiveProfile != proxy.CurrentProfileName {
		t.Errorf("active_profile = %q, want %q", pf.ActiveProfile, proxy.CurrentProfileName)
	}
	if cfg := pf.Profiles[proxy.CurrentProfileName]; cfg.Host != "adhoc" {
		t.Errorf("reserved slot host = %q, want %q", cfg.Host, "adhoc")
	}
}

// The GUI splits one apply into two halves — user-level targets in-process,
// privileged ones through a reinvoked CLI — so neither half ever names every
// available target. The flag was therefore never cleared, and unticking "Via
// daemon local" then applying watched the box tick itself straight back on.
// Only the caller holding the whole selection can answer this, which is why
// ClearViaLocal is exported and asked separately.
//
// "all" is used for the covering case because it satisfies coverage on any
// machine, whatever happens to be installed — these tests run against the
// real target registry, which cannot be faked.
func TestClearViaLocalDropsTheFlagWhenEverythingWasRewritten(t *testing.T) {
	isolateConfig(t, `{"via_local":true,"profiles":{}}`)

	if err := ClearViaLocal([]string{"all"}); err != nil {
		t.Fatalf("ClearViaLocal: %v", err)
	}
	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if pf.ViaLocal {
		t.Error("via_local continuou ligado depois de reescrever todos os alvos")
	}
}

// A partial rewrite leaves the untouched targets still pointing at the
// daemon, so the flag has to stay — clearing it there would silence the
// warning that those depend on a daemon which may not be running.
func TestClearViaLocalKeepsTheFlagOnAPartialRewrite(t *testing.T) {
	const parcial = "git"
	if proxy.SelectsAllAvailableTargets([]string{parcial}) {
		t.Skipf("nesta máquina %q sozinho já cobre todos os alvos disponíveis; o caso parcial não é observável aqui", parcial)
	}
	isolateConfig(t, `{"via_local":true,"profiles":{}}`)

	if err := ClearViaLocal([]string{parcial}); err != nil {
		t.Fatalf("ClearViaLocal: %v", err)
	}
	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if !pf.ViaLocal {
		t.Error("via_local foi limpo com uma seleção parcial — os demais alvos continuam encanados")
	}
}

// ApplyUserTargets is what the daemon calls on its own startup: it must
// reach every target that never needs root, and skip every one that does —
// those are the .deb postinst's job, not something an unprivileged process
// should attempt silently.
func TestApplyUserTargetsSkipsPrivilegedTargets(t *testing.T) {
	safe := &fakeTarget{name: "git", available: true}
	privileged := &fakeTarget{name: "apt", available: true, root: true}
	d := depsFor(safe, privileged)

	pf := &proxy.ProfileFile{LocalPort: 9999}
	rep, err := ApplyUserTargets(d, &proxy.Executor{}, pf)
	if err != nil {
		t.Fatalf("ApplyUserTargets: %v", err)
	}

	if len(safe.setCfgs) != 1 {
		t.Errorf("safe target Set called %d times, want 1", len(safe.setCfgs))
	}
	if len(privileged.setCfgs) != 0 {
		t.Errorf("privileged target Set called %d times, want 0", len(privileged.setCfgs))
	}
	got := safe.setCfgs[0]
	if got.Host != "127.0.0.1" || got.Port != "9999" {
		t.Errorf("target config = %+v, want the local daemon on port 9999", got)
	}
	// No credentials: the daemon holds the upstream, targets never see it.
	if got.Username != "" || got.Password != "" {
		t.Errorf("credentials leaked into the target: %+v", got)
	}
	if len(rep.Results) != 2 {
		t.Errorf("Report has %d results, want 2 (one per target, including the skipped one)", len(rep.Results))
	}
}

func TestApplyUserTargetsUsesDockerBridge(t *testing.T) {
	dockerConfig := &fakeTarget{name: "docker-config", available: true}
	other := &fakeTarget{name: "git", available: true}
	d := depsFor(dockerConfig, other)

	pf := &proxy.ProfileFile{LocalPort: 9999, DockerBridge: true}
	if _, err := ApplyUserTargets(d, &proxy.Executor{}, pf); err != nil {
		t.Fatalf("ApplyUserTargets: %v", err)
	}

	if len(dockerConfig.setCfgs) != 1 {
		t.Fatalf("docker-config Set called %d times, want 1", len(dockerConfig.setCfgs))
	}
	got := dockerConfig.setCfgs[0]
	if got.Host != "172.17.0.1" || got.Port != "9999" {
		t.Errorf("docker-config target config = %+v, want the bridge address on port 9999", got)
	}

	if len(other.setCfgs) != 1 {
		t.Fatalf("git Set called %d times, want 1", len(other.setCfgs))
	}
	gotOther := other.setCfgs[0]
	if gotOther.Host != "127.0.0.1" || gotOther.Port != "9999" {
		t.Errorf("git target config = %+v, want loopback on port 9999", gotOther)
	}
}

func TestApplyUserTargetsUsesGlobalNoProxy(t *testing.T) {
	safe := &fakeTarget{name: "git", available: true}
	d := depsFor(safe)
	pf := &proxy.ProfileFile{LocalPort: 8888, GlobalNoProxy: []string{"localhost", "10.0.0.0/8"}}

	if _, err := ApplyUserTargets(d, &proxy.Executor{}, pf); err != nil {
		t.Fatalf("ApplyUserTargets: %v", err)
	}
	got := safe.setCfgs[0].NoProxy
	want := []string{"localhost", "10.0.0.0/8"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("NoProxy = %v, want %v", got, want)
	}
}
