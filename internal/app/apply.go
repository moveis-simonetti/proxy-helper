package app

import (
	"errors"
	"fmt"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// ErrDaemonNotRunning marks the failure to reach the local proxy daemon. It
// is a sentinel rather than plain prose so a caller — in particular a GUI —
// can detect the condition with errors.Is instead of matching a string that
// the CLI wraps its own instruction around.
var ErrDaemonNotRunning = errors.New("the local proxy is not running")

// Deps are the seams the callers own: the CLI and the tests swap them, the
// GUI passes the real ones. They are parameters rather than package globals
// so cmd/proxy_test.go can keep swapping its own copies.
type Deps struct {
	ResolveTargets func([]string) ([]proxy.Target, error)
	DaemonActive   func() bool
	ReloadDaemon   func(*proxy.Executor) error
	BridgeAddr     func() (string, error)
	// InstallDaemon writes and starts the user unit. Only Setup needs it,
	// so callers that never run Setup may leave it nil.
	InstallDaemon func(*proxy.Executor) error
	// Notify, when set, receives each Notice as it is raised rather than only
	// at the end. The CLI uses it to keep warnings in their original
	// position: interleaved with the executor's own output, and before the
	// sudo prompt they warn about. A GUI leaves it nil and reads
	// Report.Notices when the work ends.
	Notify func(Notice)
}

// warn records n in the report and, when d.Notify is set, delivers it
// immediately — the CLI uses this to print a notice at the moment it is
// raised instead of only after everything finished.
func warn(d Deps, rep *Report, n Notice) {
	rep.Warn(n)
	if d.Notify != nil {
		d.Notify(n)
	}
}

// Apply pushes cfg into the named targets. The returned error is global: when
// it is non-nil nothing was applied. Per-target failures live in the Report.
//
// Apply takes the profile lock itself (via proxy.WithProfileLock/SaveLocked,
// on the paths that touch the profile file). Do not call it from inside code
// that is already holding that lock: flock does not nest within a process,
// so a second acquisition on another descriptor blocks forever with no error
// — a silent deadlock, not a visible one.
// profileName is the saved profile cfg came from, or "" when cfg is ad-hoc
// (typed into "proxy set --host ..." flags). It only matters on the viaLocal
// path, which has to leave something active for the daemon to read.
func Apply(d Deps, ex *proxy.Executor, profileName string, cfg proxy.Config, targetNames []string, viaLocal bool) (*Report, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, err
	}
	cfg.NoProxy = proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)

	if viaLocal {
		// The daemon only ever reads active_profile, so something has to be
		// active here. A named profile activates under its own name; only a
		// genuinely nameless config falls back to the reserved slot.
		//
		// Passing "" for a config that HAD a name is what used to demote it:
		// "proxy set --profile Trabalho --via-local" — and every Aplicar on
		// the GUI's Status page, which applies the active profile's config —
		// rewrote active_profile to "_current", losing the name the user
		// chose and leaving the GUI's profile selector with nothing to show.
		if profileName != "" {
			if err := pf.On(profileName); err != nil {
				return nil, err
			}
		} else {
			pf.SetCurrent(cfg)
		}
		return ApplyViaLocal(d, ex, pf, cfg, targetNames)
	}

	targets, err := d.ResolveTargets(targetNames)
	if err != nil {
		return nil, err
	}

	rep := &Report{}
	noticeUnreachablePassword(d, rep, cfg)

	// The targets are about to hold the real upstream again, so the
	// plumbing flag must stop claiming they point at the loopback. See
	// ClearViaLocal for why coverage decides that and not the apply alone.
	if !ex.DryRun {
		if err := ClearViaLocal(targetNames); err != nil {
			return nil, err
		}
	}

	applied := proxy.TargetConfig(cfg, false, 0)
	setEach(d, rep, ex, targets, func(proxy.Target) proxy.Config { return applied })
	noticeDockerNeedsRestart(d, rep, true)
	return rep, nil
}

// eachTarget runs op against every available target, recording an
// availability skip or a sudo notice ahead of it as needed, and the
// resulting outcome (ok, or OutcomeFailed on error). setEach and Clear are
// both instances of this shape, differing only in the operation, the
// outcome that marks success, and needsRoot.
//
// needsRoot decides, per target, whether *this* op will actually need
// elevation — it is not just t.RequiresRoot(), because that single bool
// can conflate two different operations (see gnome's RequiresRoot doc
// comment: true only because Unset's PackageKit workaround needs it, while
// Set never does). setEach and Clear each pass the honest answer for their
// own operation.
//
// The notice is further gated on ex.Escalation != proxy.EscalateNone:
// EscalateNone (used by the GUI for session-scoped targets, and by any
// other in-process caller) refuses to elevate outright — see Executor's
// doc comment — so no password prompt can ever appear, and warning about
// one would be a lie regardless of what needsRoot says.
func eachTarget(d Deps, rep *Report, ex *proxy.Executor, targets []proxy.Target, op func(proxy.Target) error, ok Outcome, needsRoot func(proxy.Target) bool) {
	for _, t := range targets {
		if !t.Available() {
			// The target already knows why it is unavailable: every target's
			// Status returns that reason as its first check, before any file
			// or command I/O that Available() being false would make pointless.
			detail := ""
			if st, err := t.Status(ex, false); err == nil {
				detail = st.Detail
			}
			rep.Add(Result{Target: t.Name(), Outcome: OutcomeSkipped, Detail: detail})
			continue
		}
		if needsRoot(t) && !proxy.IsRoot() && !ex.DryRun && ex.Escalation != proxy.EscalateNone {
			warn(d, rep, Notice{Kind: NoticeNeedsSudo, Target: t.Name()})
		}
		if err := op(t); err != nil {
			rep.Add(Result{Target: t.Name(), Outcome: OutcomeFailed, Err: err})
			continue
		}
		rep.Add(Result{Target: t.Name(), Outcome: ok})
	}
}

// setNeedsRoot is the needsRoot answer for Set: a session-scoped target's
// Set never needs root, even when RequiresRoot() is true for a reason that
// only applies to its Unset (see gnome).
func setNeedsRoot(t proxy.Target) bool {
	return t.RequiresRoot() && !t.SessionScoped()
}

// setEach lets the caller decide the config per target, which is what the
// Docker targets need when the plumbing is in place.
func setEach(d Deps, rep *Report, ex *proxy.Executor, targets []proxy.Target, configFor func(proxy.Target) proxy.Config) {
	eachTarget(d, rep, ex, targets, func(t proxy.Target) error {
		return t.Set(ex, configFor(t))
	}, OutcomeApplied, setNeedsRoot)
}

// ApplyViaLocal writes the plumbing: it points the targets at the local
// daemon and records that fact. The caller has already decided which profile
// the daemon should serve by setting pf.ActiveProfile; this function never
// inspects or changes that choice.
func ApplyViaLocal(d Deps, ex *proxy.Executor, pf *proxy.ProfileFile, cfg proxy.Config, targetNames []string) (*Report, error) {
	targets, err := d.ResolveTargets(targetNames)
	if err != nil {
		return nil, err
	}
	if !d.DaemonActive() && !ex.DryRun {
		return nil, fmt.Errorf("%w; run \"proxy serve install\" first", ErrDaemonNotRunning)
	}

	pf.ViaLocal = true
	if !ex.DryRun {
		// SaveLocked, not WithProfileLock: pf is the caller's ProfileFile,
		// already carrying the ActiveProfile it just set. Reloading here
		// would silently discard that choice.
		if err := proxy.SaveLocked(pf); err != nil {
			return nil, err
		}
		if err := d.ReloadDaemon(ex); err != nil {
			return nil, err
		}
	}

	port := pf.EffectiveLocalPort()
	loopback := proxy.TargetConfig(cfg, true, port)
	rep := &Report{}

	// Docker's settings are read from inside containers, where 127.0.0.1 is
	// the container itself. Those targets need an address a container can
	// reach, which only exists when the daemon listens on the bridge too.
	dockerCfg := loopback
	if pf.DockerBridge {
		bridge, err := d.BridgeAddr()
		if err != nil {
			return nil, fmt.Errorf("docker_bridge is enabled but the bridge is unusable: %w", err)
		}
		dockerCfg = proxy.TargetConfigAt(cfg, true, port, bridge)
	} else {
		noticeDockerLoopback(d, rep, targets)
	}

	setEach(d, rep, ex, targets, func(t proxy.Target) proxy.Config {
		if serve.IsDockerTarget(t.Name()) {
			return dockerCfg
		}
		return loopback
	})
	noticeDockerNeedsRestart(d, rep, true)
	return rep, nil
}

// ApplyUserTargets points every target that never needs root at the local
// daemon. It exists for the daemon's own startup: once proxy serve is
// listening, every unprivileged tool on the machine should be able to reach
// it without waiting for a person to run anything — no sudo prompt, so no
// reason to gate it behind one.
//
// Unlike ApplyViaLocal, it does not check that a daemon is reachable (the
// caller IS the daemon, mid-startup, and systemd may not have marked the
// unit "active" yet) and it never touches a target whose Set needs root
// (apt, system-env, dockerd, snap): those are the .deb postinst's job,
// which already runs as root at install time. An unprivileged process
// silently attempting them would just fail, or worse, prompt.
func ApplyUserTargets(d Deps, ex *proxy.Executor, pf *proxy.ProfileFile) (*Report, error) {
	all, err := d.ResolveTargets([]string{"all"})
	if err != nil {
		return nil, err
	}
	var targets []proxy.Target
	var skipped []proxy.Target
	for _, t := range all {
		if !setNeedsRoot(t) {
			targets = append(targets, t)
		} else {
			skipped = append(skipped, t)
		}
	}

	port := pf.EffectiveLocalPort()
	cfg := proxy.Config{NoProxy: pf.EffectiveGlobalNoProxy()}
	loopback := proxy.TargetConfig(cfg, true, port)

	rep := &Report{}

	// Same reasoning as ApplyViaLocal: docker-config is read from inside
	// containers, where 127.0.0.1 is the container itself, not the host.
	dockerCfg := loopback
	if pf.DockerBridge {
		bridge, err := d.BridgeAddr()
		if err != nil {
			return nil, fmt.Errorf("docker_bridge is enabled but the bridge is unusable: %w", err)
		}
		dockerCfg = proxy.TargetConfigAt(cfg, true, port, bridge)
	} else {
		noticeDockerLoopback(d, rep, targets)
	}

	setEach(d, rep, ex, targets, func(t proxy.Target) proxy.Config {
		if serve.IsDockerTarget(t.Name()) {
			return dockerCfg
		}
		return loopback
	})

	// Record the privileged targets as skipped
	for _, t := range skipped {
		rep.Add(Result{Target: t.Name(), Outcome: OutcomeSkipped})
	}

	return rep, nil
}

// Clear removes the proxy settings from the named targets.
//
// Like Apply, Clear takes the profile lock itself (via
// proxy.WithProfileLock) when the cleared target set covers everything. Do
// not call it from inside code that is already holding that lock: flock
// does not nest within a process, so a second acquisition on another
// descriptor blocks forever with no error — a silent deadlock, not a
// visible one.
func Clear(d Deps, ex *proxy.Executor, targetNames []string) (*Report, error) {
	targets, err := d.ResolveTargets(targetNames)
	if err != nil {
		return nil, err
	}

	// Only a full unset can honestly say the plumbing is gone. Clearing one
	// target still leaves the others pointing at the loopback.
	//
	// SelectsAllAvailableTargets, not SelectsAllTargets: see Apply's
	// equivalent comment above.
	if !ex.DryRun && proxy.SelectsAllAvailableTargets(targetNames) {
		if err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			pf.ViaLocal = false
			return nil
		}); err != nil {
			return nil, err
		}
	}

	rep := &Report{}
	// Unlike Set, Unset keeps the plain t.RequiresRoot(): gnome's Unset
	// genuinely needs root for the PackageKit workaround when it applies,
	// so a CLI "proxy unset --targets gnome" must still get the notice.
	eachTarget(d, rep, ex, targets, func(t proxy.Target) error {
		return t.Unset(ex)
	}, OutcomeCleared, proxy.Target.RequiresRoot)
	noticeDockerNeedsRestart(d, rep, false)
	return rep, nil
}

// noticeUnreachablePassword reports a configuration that cannot work: the
// password lives in a file or environment variable, which only the daemon
// resolves, but the config is being written straight into each tool.
func noticeUnreachablePassword(d Deps, rep *Report, cfg proxy.Config) {
	if cfg.Password != "" || cfg.Username == "" {
		return
	}
	source := "password_file"
	if cfg.PasswordFile == "" {
		if cfg.PasswordEnv == "" {
			return
		}
		source = "password_env"
	}
	warn(d, rep, Notice{Kind: NoticeUnreachablePassword, Args: map[string]string{"source": source}})
}

// noticeDockerLoopback flags the failure that is otherwise baffling: image
// pulls keep working, because dockerd runs on the host, while any build step
// that needs the network dies on a connection refused to 127.0.0.1.
func noticeDockerLoopback(d Deps, rep *Report, targets []proxy.Target) {
	for _, t := range targets {
		if !serve.IsDockerTarget(t.Name()) || !t.Available() {
			continue
		}
		warn(d, rep, Notice{Kind: NoticeDockerLoopback, Target: t.Name()})
		return
	}
}

// noticeDockerNeedsRestart flags the drop-in dockerd just wrote (or
// removed): systemd only reads it after `systemctl restart docker`, and
// that is never done automatically because it disrupts running containers.
// It is scoped to the dockerd target alone — docker-config touches
// ~/.docker/config.json, which every docker command reads fresh, so it never
// needs a restart. applied distinguishes Apply (targets holds a successful
// Set) from Clear (targets holds a successful Unset), since the two read
// differently to the user.
func noticeDockerNeedsRestart(d Deps, rep *Report, applied bool) {
	wantOutcome := OutcomeCleared
	op := "clear"
	if applied {
		wantOutcome = OutcomeApplied
		op = "apply"
	}
	for _, res := range rep.Results {
		if res.Target != "dockerd" || res.Outcome != wantOutcome {
			continue
		}
		warn(d, rep, Notice{Kind: NoticeDockerNeedsRestart, Target: "dockerd", Args: map[string]string{"op": op}})
		return
	}
}

// ClearViaLocal drops the "targets point at the local daemon" flag, but only
// when names cover every AVAILABLE target.
//
// Coverage is the condition because the flag describes reality, not
// intention: rewriting some targets with the real upstream leaves the rest
// still pointing at the daemon, and clearing the flag there would silence
// the warning that those depend on a daemon which may not be running.
// SelectsAllAvailableTargets, not SelectsAllTargets: an unavailable target
// was never plumbed (eachTarget skips it outright), so it should not have to
// be named for this to count as complete.
//
// It is exported because the GUI needs it. The GUI splits one apply into two
// halves — user-level targets in-process, privileged ones through a
// reinvoked CLI — so neither half ever covers everything on its own, and the
// flag was never cleared: unticking "Via daemon local", applying, and
// watching the box tick itself straight back on when the page reloaded.
// Only the caller that made the whole selection can answer the coverage
// question, so that caller gets to ask it.
func ClearViaLocal(names []string) error {
	if !proxy.SelectsAllAvailableTargets(names) {
		return nil
	}
	return proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
		lpf.ViaLocal = false
		return nil
	})
}
