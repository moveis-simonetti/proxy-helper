// Package-level note: this file holds the orchestration behind activating
// and deactivating profiles. It is the "profile" half of what apply.go does
// for targets, and it exists here rather than in cmd/ for the same reason:
// the GUI needs the identical decision sequence, and two copies would drift.
package app

import (
	"fmt"

	"proxy-helper/internal/proxy"
)

// OnResult is what On did. Profile is the profile that ended up active; the
// caller words the message.
type OnResult struct {
	Profile string
}

// OffResult is what Off did. AlreadyOff is true when there was no active
// profile to clear — not an error, and nothing was reloaded. Previous is the
// profile that was active, empty when AlreadyOff.
type OffResult struct {
	Previous   string
	AlreadyOff bool
}

// On restores proxying through name, or through the last profile used when
// name is empty. It touches no target: with the targets pointing at the local
// daemon, which upstream the daemon uses is pure state, so this needs no
// privilege and takes effect as soon as the daemon re-reads its config.
func On(d Deps, ex *proxy.Executor, name string) (OnResult, error) {
	var res OnResult
	err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		if err := pf.On(name); err != nil {
			return err
		}
		res.Profile = pf.ActiveProfile
		return nil
	})
	if err != nil {
		return OnResult{}, err
	}
	// Outside the lock: ReloadDaemon never touches the profile file, but
	// flock does not nest within a process, so keeping it out is the rule
	// rather than a case-by-case judgement.
	if err := d.ReloadDaemon(ex); err != nil {
		return OnResult{}, err
	}
	return res, nil
}

// Off clears the active profile so the daemon sends everything direct. Like
// On, it touches no target.
func Off(d Deps, ex *proxy.Executor) (OffResult, error) {
	var res OffResult
	err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		if pf.ActiveProfile == "" {
			res.AlreadyOff = true
			return nil
		}
		// Off, not a bare assignment: it records the profile in
		// last_profile so a later On has something to restore.
		pf.Off()
		res.Previous = pf.LastProfile
		return nil
	})
	if err != nil {
		return OffResult{}, err
	}
	if res.AlreadyOff {
		return res, nil
	}
	if err := d.ReloadDaemon(ex); err != nil {
		return OffResult{}, err
	}
	return res, nil
}

// EnableResult is what Enable did. Report is nil when TargetsUntouched is
// true, because no target was touched.
type EnableResult struct {
	Report *Report
	// TargetsUntouched is true when the targets already pointed at the local
	// daemon, so switching profiles changed state only.
	TargetsUntouched bool
}

// Enable applies a saved profile and marks it active. It has three routes,
// and which one runs is not a preference — each exists to avoid a specific
// broken end state:
//
//  1. viaLocal asked for explicitly: write the plumbing, and record the
//     profile's NAME as active. Not the reserved "_current" copy, which would
//     go stale the moment the profile is edited.
//  2. The plumbing is already in place (pf.ViaLocal): the targets already
//     reach the daemon, so this is pure state. Writing to a target here would
//     tear the plumbing down and put the upstream credential into every
//     tool's config file.
//  3. Otherwise: record the activation BEFORE touching any target. Targets
//     fail for mundane reasons, and returning early used to leave the machine
//     with every target configured but no active profile.
func Enable(d Deps, ex *proxy.Executor, name string, targetNames []string, viaLocal bool) (EnableResult, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return EnableResult{}, err
	}
	cfg, exists := pf.Get(name)
	if !exists {
		return EnableResult{}, fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
	}

	// Route 1.
	if viaLocal {
		cfg.NoProxy = proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)
		// In memory only: ApplyViaLocal is what persists it, and it holds
		// the lock while doing so.
		pf.ActiveProfile = name
		rep, err := ApplyViaLocal(d, ex, pf, cfg, targetNames)
		if err != nil {
			return EnableResult{}, err
		}
		return EnableResult{Report: rep}, nil
	}

	// Route 2.
	if pf.ViaLocal {
		if ex.DryRun {
			rep := &Report{}
			n := Notice{Kind: NoticeProfileAlreadyPlumbed, Args: map[string]string{"profile": name}}
			warn(d, rep, n)
			return EnableResult{Report: rep, TargetsUntouched: true}, nil
		}
		if err := proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
			lpf.ActiveProfile = name
			return nil
		}); err != nil {
			return EnableResult{}, err
		}
		if err := d.ReloadDaemon(ex); err != nil {
			return EnableResult{}, err
		}
		return EnableResult{TargetsUntouched: true}, nil
	}

	// Route 3.
	if !ex.DryRun {
		if err := proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
			lpf.ActiveProfile = name
			return nil
		}); err != nil {
			return EnableResult{}, err
		}
		if err := d.ReloadDaemon(ex); err != nil {
			return EnableResult{}, err
		}
	}
	// Apply runs after the lock above is released: it may take the profile
	// lock itself, and flock does not nest within a process.
	rep, err := Apply(d, ex, cfg, targetNames, false)
	if err != nil {
		return EnableResult{}, err
	}
	return EnableResult{Report: rep}, nil
}

// Disable clears the active profile's settings from the targets and drops the
// activation. name, when non-empty, must be the active profile: it is a guard
// against disabling something other than what the user meant.
func Disable(d Deps, ex *proxy.Executor, name string, targetNames []string) (*Report, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, err
	}
	if pf.ActiveProfile == "" {
		return nil, fmt.Errorf("no profile is currently enabled")
	}
	if name != "" && name != pf.ActiveProfile {
		return nil, fmt.Errorf("profile %q is not the active one (active: %q)", name, pf.ActiveProfile)
	}

	// Clear runs, and finishes, before the lock below is taken: it may take
	// the profile lock itself, and flock does not nest within a process.
	rep, err := Clear(d, ex, targetNames)
	if err != nil {
		return nil, err
	}
	if ex.DryRun {
		return rep, nil
	}
	// A target that failed to drop the proxy is still holding it. Dropping
	// active_profile anyway would tell the user the profile is off while a
	// target still points at the upstream — the exact partial state this
	// tool exists to prevent. The state only falls once every target really
	// let go.
	if rep.Err() != nil {
		return rep, nil
	}

	if err := proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
		// Off, not a bare assignment: it remembers the profile so that a
		// later On has something to restore.
		lpf.Off()
		return nil
	}); err != nil {
		return nil, err
	}
	if err := d.ReloadDaemon(ex); err != nil {
		return nil, err
	}
	return rep, nil
}
