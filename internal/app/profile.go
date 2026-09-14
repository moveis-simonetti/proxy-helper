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
		// Keyed on the mode, not on ActiveProfile: the profile stays
		// selected while traffic goes direct, so an empty selection no
		// longer means "already off" — and a config with a profile
		// selected but mode direct is exactly the already-off case.
		if pf.EffectiveMode() == proxy.ModeDirect {
			res.AlreadyOff = true
			return nil
		}
		res.Previous = pf.ActiveProfile
		pf.Off()
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

// DisableResult is what Disable did. It mirrors EnableResult: Report is nil
// when TargetsUntouched is true, because no target was touched.
type DisableResult struct {
	Report *Report
	// TargetsUntouched is true when the targets already pointed at the local
	// daemon, so switching off changed state only.
	TargetsUntouched bool
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
//     reach the daemon, so this is pure state. Writing to a proxy-URL target
//     here would tear the plumbing down and put the upstream credential into
//     every tool's config file — with one deliberate exception: nm-
//     connectivity's content is the profile's own, not the daemon's address,
//     so it still gets reapplied here when configured (see the route body).
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
		// nm-connectivity is not "pure state" the way every other target is
		// here: the others just point at the local daemon regardless of
		// which profile is active, so re-writing them would be redundant at
		// best (Set with the wrong cfg would be actively harmful — see the
		// comment on Enable). nm-connectivity's correct content DOES depend
		// on which profile is active (it is the profile's own
		// ConnectivityCheckURL/Response), so switching profiles has to
		// reapply it even on this otherwise-untouched route — including
		// switching TO a profile that leaves it unset, which must still
		// clean up a previous profile's file rather than leave
		// NetworkManager checking a network that is no longer active.
		//
		// The common case — no profile involved has ever configured this —
		// stays a true no-op: Status is a plain, unprivileged file check
		// (see nmConnectivityTarget.Status), so asking first costs nothing
		// and keeps TargetsUntouched honest for every profile that never
		// touches this feature, matching what it promises callers.
		targets, err := d.ResolveTargets([]string{"nm-connectivity"})
		if err != nil {
			return EnableResult{}, err
		}
		staleFile := false
		for _, t := range targets {
			if st, statusErr := t.Status(ex, false); statusErr == nil && st.Enabled {
				staleFile = true
			}
		}
		if cfg.ConnectivityCheckURL == "" && !staleFile {
			return EnableResult{TargetsUntouched: true}, nil
		}
		rep := &Report{}
		setEach(d, rep, ex, targets, func(proxy.Target) proxy.Config { return cfg })
		return EnableResult{Report: rep}, nil
	}

	// Route 3.
	// This route writes the upstream (credentials included) straight into
	// every target — the "--no-via-local" path. The GUI never calls Enable
	// with viaLocal=false; with targets always pointed at the daemon (see
	// ApplyUserTargets), this is a script/CLI-only escape hatch now.
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
	rep, err := Apply(d, ex, name, cfg, targetNames, false)
	if err != nil {
		return EnableResult{}, err
	}
	return EnableResult{Report: rep}, nil
}

// Disable stops proxying through the active profile. name, when non-empty,
// must be the active profile: it is a guard against disabling something
// other than what the user meant.
//
// It has two routes, and which one runs depends on where the proxy actually
// lives. With the targets pointing at the local daemon there is nothing in
// them to clear — they name the daemon, not the upstream — so the whole job
// is switching the daemon to direct: no target rewritten, no password asked,
// instant. Without that plumbing the targets each hold the upstream, and the
// only way to stop using it is to clear all of them.
//
// The plumbing itself is never undone here. Taking the targets off the
// daemon is "proxy unset", which is the documented recovery path rather
// than a daily action.
func Disable(d Deps, ex *proxy.Executor, name string, targetNames []string) (DisableResult, error) {
	var res DisableResult
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return res, err
	}
	// Both halves mean "nothing to disable": no profile selected, or one
	// selected but already routing direct.
	if pf.ActiveProfile == "" || !pf.EffectiveMode().Forwards() {
		return res, fmt.Errorf("no profile is currently enabled")
	}
	if name != "" && name != pf.ActiveProfile {
		return res, fmt.Errorf("profile %q is not the active one (active: %q)", name, pf.ActiveProfile)
	}

	// The cheap route: the targets name the daemon, not the upstream, so
	// there is nothing in them to clear.
	if pf.ViaLocal {
		if _, err := SetMode(d, ex, proxy.ModeDirect); err != nil {
			return res, err
		}
		res.TargetsUntouched = true
		return res, nil
	}

	// Clear runs, and finishes, before the lock below is taken: it may take
	// the profile lock itself, and flock does not nest within a process.
	rep, err := Clear(d, ex, targetNames)
	if err != nil {
		return res, err
	}
	res.Report = rep
	if ex.DryRun {
		return res, nil
	}
	// A target that failed to drop the proxy is still holding it. Dropping
	// active_profile anyway would tell the user the profile is off while a
	// target still points at the upstream — the exact partial state this
	// tool exists to prevent. The state only falls once every target really
	// let go.
	if rep.Err() != nil {
		return res, nil
	}

	if err := proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
		// Off, not a bare assignment: it remembers the profile so that a
		// later On has something to restore.
		lpf.Off()
		return nil
	}); err != nil {
		return res, err
	}
	if err := d.ReloadDaemon(ex); err != nil {
		return res, err
	}
	return res, nil
}
