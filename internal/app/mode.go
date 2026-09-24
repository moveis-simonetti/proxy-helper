package app

import "proxy-helper/internal/proxy"

// ModeResult is what SetMode did. Unchanged is true when the config already
// held that mode — not an error, and nothing was reloaded.
type ModeResult struct {
	Mode      proxy.Mode
	Previous  proxy.Mode
	Unchanged bool
}

// SetMode records the daemon's routing mode and asks it to re-read the
// config.
//
// This is the cheap toggle the whole design turns on: with the targets
// pointing at the local daemon, switching between forwarding and direct is
// pure state plus a SIGHUP. No target is rewritten, so nothing asks for a
// password and no already-running application has to be relaunched.
//
// On/Off are thin wrappers over this — they are the friendly names for two
// of the three modes, and keeping the state handling in one place is what
// stops them from drifting apart.
func SetMode(d Deps, ex *proxy.Executor, m proxy.Mode) (ModeResult, error) {
	var res ModeResult
	var snapshot *proxy.ProfileFile
	err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		res.Previous = pf.EffectiveMode()
		if res.Previous == m {
			res.Mode = m
			res.Unchanged = true
			return nil
		}
		// SetMode refuses a forwarding mode with nothing selected, so an
		// invalid request fails here, before anything is written.
		if err := pf.SetMode(m); err != nil {
			return err
		}
		res.Mode = m
		snapshot = pf
		return nil
	})
	if err != nil {
		return ModeResult{}, err
	}
	if res.Unchanged {
		// No SIGHUP for a no-op: the daemon would re-read the same file.
		return res, nil
	}
	// Outside the lock: flock does not nest within this process, and
	// ReloadDaemon has no business holding the profile file anyway.
	if err := d.ReloadDaemon(ex); err != nil {
		return ModeResult{}, err
	}
	syncConnectivityCheck(d, ex, snapshot, res.Previous, res.Mode)
	return res, nil
}

// syncConnectivityCheck keeps NetworkManager's own connectivity check in
// step with a transition into or out of direct mode. nm-connectivity's
// whole point is testing something reachable only while traffic is
// actually flowing through the proxy: going direct means nothing is
// proxied any more, so the check must be cleared — NM correctly reporting
// "limited" on a proxy-only network is the honest answer while direct is
// active, not a bug to hide. Leaving direct means traffic is proxied
// again, so the check must come back.
//
// Best-effort and silent on failure (e.g. a declined sudo prompt): mode
// switching — and On/Off, which share this — is meant to always succeed
// instantly for every profile that never configures this feature. This is
// the one deliberate, opt-in exception to "no target touched," not a new
// way for those commands to fail outright.
func syncConnectivityCheck(d Deps, ex *proxy.Executor, pf *proxy.ProfileFile, oldMode, newMode proxy.Mode) {
	if pf == nil {
		return
	}
	goingDirect := newMode == proxy.ModeDirect && oldMode != proxy.ModeDirect
	leavingDirect := oldMode == proxy.ModeDirect && newMode != proxy.ModeDirect
	if !goingDirect && !leavingDirect {
		return
	}
	cfg, ok := pf.Get(pf.ActiveProfile)
	if !ok || cfg.ConnectivityCheckURL == "" {
		return
	}
	targets, err := d.ResolveTargets([]string{"nm-connectivity"})
	if err != nil || len(targets) == 0 {
		return
	}
	applyCfg := cfg
	if goingDirect {
		applyCfg = proxy.Config{}
	}
	rep := &Report{}
	setEach(d, rep, ex, targets, func(proxy.Target) proxy.Config { return applyCfg })
}

// CurrentMode reports the configured mode and the selected profile, for
// commands and UI that only want to display them.
func CurrentMode() (proxy.Mode, string, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return "", "", err
	}
	return pf.EffectiveMode(), pf.ActiveProfile, nil
}
