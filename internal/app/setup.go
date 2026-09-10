package app

import (
	"fmt"

	"proxy-helper/internal/proxy"
)

// SetupOptions describes the machine setup to reach.
type SetupOptions struct {
	// Profile is the name to save under and select.
	Profile string
	// Config is the upstream's connection details. Leave it zero to reuse a
	// profile that is already saved.
	Config proxy.Config
	// Targets is what to point at the daemon; nil means all.
	Targets []string
	// Mode is the routing mode to leave behind. Empty means auto, which is
	// the point of setting the machine up this way.
	Mode proxy.Mode
	// Progress, when set, receives one line per step as it happens.
	//
	// It exists for ordering, not decoration: the Executor streams its own
	// output (dry-run previews, sudo prompts) as each target is written, so
	// a caller that printed the steps from the returned result would show
	// "daemon installed" after the target output it logically precedes.
	Progress func(string)
}

// SetupResult is what Setup did, step by step, so the caller can show a
// checklist rather than a wall of output.
type SetupResult struct {
	// DaemonInstalled is true when Setup installed it just now; false when
	// it was already running.
	DaemonInstalled bool
	Profile         string
	Mode            proxy.Mode
	Port            int
	Report          *Report
}

// Setup takes a machine from nothing to the arrangement the rest of this
// tool is designed around: the daemon running, every target pointing at it,
// and a routing mode set.
//
// It exists because that arrangement was previously assembled by hand out of
// three commands in the right order, with "proxy serve install" first and
// the --via-local flag on the second — a sequence nobody discovers on their
// own. Getting it wrong leaves the expensive path in place: the daily
// on/off keeps rewriting thirteen targets and asking for a password, which
// is exactly what pointing them at the daemon was meant to stop.
//
// The steps are ordered by what depends on what: ApplyViaLocal refuses to
// run without a live daemon, and the mode cannot be set before a profile is
// selected.
func Setup(d Deps, ex *proxy.Executor, o SetupOptions) (*SetupResult, error) {
	if o.Profile == "" {
		return nil, fmt.Errorf("a profile name is required")
	}
	mode := o.Mode
	if mode == "" {
		mode = proxy.ModeAuto
	}
	if !proxy.ValidMode(mode) {
		return nil, fmt.Errorf("unknown mode %q (want auto, upstream or direct)", mode)
	}
	targets := o.Targets
	if len(targets) == 0 {
		targets = []string{"all"}
	}

	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, err
	}

	// Resolve the config before touching anything: an unknown profile with
	// no connection details is a mistake, and finding out after installing
	// a unit would leave the machine half set up.
	cfg := o.Config
	if cfg.Host == "" {
		saved, ok := pf.Get(o.Profile)
		if !ok {
			return nil, fmt.Errorf("profile %q is not saved; pass the connection details (--host, --port, ...)", o.Profile)
		}
		cfg = saved
	}

	res := &SetupResult{Profile: o.Profile, Mode: mode}
	step := func(format string, args ...any) {
		if o.Progress != nil {
			o.Progress(fmt.Sprintf(format, args...))
		}
	}

	// An already-running daemon is left alone: setup is meant to be safe to
	// re-run, and reinstalling would restart the proxy under live
	// connections for no reason.
	if !d.DaemonActive() {
		if d.InstallDaemon == nil {
			return nil, fmt.Errorf("the daemon is not running and this caller cannot install it; run \"proxy serve install\" first")
		}
		if err := d.InstallDaemon(ex); err != nil {
			return nil, fmt.Errorf("installing the daemon: %w", err)
		}
		res.DaemonInstalled = !ex.DryRun
		step("daemon instalado e ativo em 127.0.0.1:%d", pf.EffectiveLocalPort())
	} else {
		step("daemon já ativo em 127.0.0.1:%d", pf.EffectiveLocalPort())
	}

	// Save, select and set the mode in one pass, so a failure never leaves
	// a profile selected with a mode that contradicts it.
	if !ex.DryRun {
		err = proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
			if lpf.Profiles == nil {
				lpf.Profiles = map[string]proxy.Config{}
			}
			if o.Config.Host != "" {
				lpf.Profiles[o.Profile] = o.Config
			}
			if err := lpf.SelectProfile(o.Profile); err != nil {
				return err
			}
			return lpf.SetMode(mode)
		})
		if err != nil {
			return nil, err
		}
		// Re-read so ApplyViaLocal works from the state just committed
		// rather than the stale copy loaded above.
		if pf, err = proxy.LoadProfiles(); err != nil {
			return nil, err
		}
	} else {
		pf.Profiles[o.Profile] = cfg
		pf.ActiveProfile = o.Profile
		pf.Mode = string(mode)
	}
	res.Port = pf.EffectiveLocalPort()
	step("perfil %q salvo e selecionado, modo %s", o.Profile, mode)
	step("apontando os alvos para o daemon:")

	// ApplyViaLocal takes the profile lock itself, so it must run outside
	// the block above: flock does not nest within a process.
	cfg.NoProxy = proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)
	rep, err := ApplyViaLocal(d, ex, pf, cfg, targets)
	if err != nil {
		return nil, err
	}
	res.Report = rep
	return res, nil
}
