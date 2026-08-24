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
func Apply(d Deps, ex *proxy.Executor, cfg proxy.Config, targetNames []string, viaLocal bool) (*Report, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, err
	}
	cfg.NoProxy = proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)

	if viaLocal {
		// An ad-hoc config is not a named profile, so it goes in the
		// reserved slot: the daemon only ever reads active_profile.
		pf.SetCurrent(cfg)
		return ApplyViaLocal(d, ex, pf, cfg, targetNames)
	}

	targets, err := d.ResolveTargets(targetNames)
	if err != nil {
		return nil, err
	}

	rep := &Report{}
	noticeUnreachablePassword(d, rep, cfg)

	// The targets are about to hold the real upstream again, so the plumbing
	// flag must stop claiming they point at the loopback — but only when this
	// covers every target. A partial set (say, just the Docker ones) leaves
	// the rest plumbed, and clearing the flag there would silence the warning
	// that those targets depend on a daemon that may not be running.
	if !ex.DryRun && proxy.SelectsAllTargets(targetNames) {
		if err := proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
			lpf.ViaLocal = false
			return nil
		}); err != nil {
			return nil, err
		}
	}

	applied := proxy.TargetConfig(cfg, false, 0)
	setEach(d, rep, ex, targets, func(proxy.Target) proxy.Config { return applied })
	return rep, nil
}

// eachTarget runs op against every available target, recording an
// availability skip or a sudo notice ahead of it as needed, and the
// resulting outcome (ok, or OutcomeFailed on error). setEach and Clear are
// both instances of this shape, differing only in the operation and the
// outcome that marks success.
func eachTarget(d Deps, rep *Report, ex *proxy.Executor, targets []proxy.Target, op func(proxy.Target) error, ok Outcome) {
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
		if t.RequiresRoot() && !proxy.IsRoot() && !ex.DryRun {
			warn(d, rep, Notice{Kind: NoticeNeedsSudo, Target: t.Name()})
		}
		if err := op(t); err != nil {
			rep.Add(Result{Target: t.Name(), Outcome: OutcomeFailed, Err: err})
			continue
		}
		rep.Add(Result{Target: t.Name(), Outcome: ok})
	}
}

// setEach lets the caller decide the config per target, which is what the
// Docker targets need when the plumbing is in place.
func setEach(d Deps, rep *Report, ex *proxy.Executor, targets []proxy.Target, configFor func(proxy.Target) proxy.Config) {
	eachTarget(d, rep, ex, targets, func(t proxy.Target) error {
		return t.Set(ex, configFor(t))
	}, OutcomeApplied)
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
	if !ex.DryRun && proxy.SelectsAllTargets(targetNames) {
		if err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			pf.ViaLocal = false
			return nil
		}); err != nil {
			return nil, err
		}
	}

	rep := &Report{}
	eachTarget(d, rep, ex, targets, func(t proxy.Target) error {
		return t.Unset(ex)
	}, OutcomeCleared)
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
