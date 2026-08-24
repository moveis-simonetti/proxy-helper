package app

import (
	"errors"

	"proxy-helper/internal/proxy"
)

// SetGlobalNoProxy stores the global no-proxy list and tells a running daemon
// to re-read it.
//
// The reload is the whole reason this lives here rather than being two lines
// at each call site. With "--via-local" the targets only hold the loopback
// address, so the no-proxy list that actually decides where traffic goes is
// the one the daemon holds in memory. Writing config.json without a reload
// leaves the daemon routing by the previous list: the setting looks saved,
// the file says it is saved, and nothing changes. "proxy profile remove"
// already reloads for the same reason — it also changes where traffic goes.
//
// A nil list means "no global list configured", which is not the same as an
// empty one: EffectiveGlobalNoProxy (internal/proxy/profiles.go) falls back to
// DefaultGlobalNoProxy only on nil, while a non-nil empty slice means "no host
// bypasses the proxy". Callers that want the default back pass nil.
//
// The returned slice is the effective list after the write, so callers can
// show what actually took effect rather than what was typed.
func SetGlobalNoProxy(d Deps, ex *proxy.Executor, list []string) ([]string, error) {
	var effective []string
	err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		pf.GlobalNoProxy = list
		effective = pf.EffectiveGlobalNoProxy()
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Outside the lock: flock does not nest within a process, and
	// ReloadDaemon shells out to systemctl, which has no business running
	// while the config file is held.
	if err := d.ReloadDaemon(ex); err != nil {
		return nil, err
	}
	return effective, nil
}

// ErrRejected aborts a SaveProfile write without being an error the caller
// has to explain: the resolve callback already knows why, and words it in its
// own language. Returning any other error from resolve propagates unchanged.
var ErrRejected = errors.New("profile write rejected")

// SaveProfile writes one profile and, when it is the active one, tells a
// running daemon to re-read it.
//
// The reload matters for every field, not just the no-proxy list: with
// "--via-local" the daemon resolves the active profile's host, port,
// credentials and no-proxy itself (internal/serve/state.go), so editing any
// of them without a reload leaves the daemon proxying through the previous
// values while config.json shows the new ones.
//
// Only the active profile triggers a reload. Editing some other saved profile
// changes nothing the daemon is currently using, and a pointless SIGHUP would
// log a reload the user cannot explain.
//
// resolve decides what to write, and runs inside the profile lock with the
// profiles as they are on disk. That is what makes a duplicate-name check or
// a patch-the-existing-fields edit atomic with the write rather than
// advisory. Returning ErrRejected aborts without writing; any other error
// propagates unchanged, so the caller's own wording survives.
func SaveProfile(d Deps, ex *proxy.Executor, resolve func(existing map[string]proxy.Config) (string, proxy.Config, error)) error {
	var wasActive bool
	var resolveErr error

	err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		name, cfg, rErr := resolve(pf.Profiles)
		if rErr != nil {
			// Recorded, not returned: WithProfileLock would treat a
			// deliberate rejection as a failed write, and the two need
			// different handling by the caller.
			resolveErr = rErr
			return nil
		}
		pf.Profiles[name] = cfg
		wasActive = pf.ActiveProfile == name
		return nil
	})
	if err != nil {
		return err
	}
	if resolveErr != nil {
		return resolveErr
	}
	if !wasActive {
		return nil
	}
	// Outside the lock: flock does not nest within a process.
	return d.ReloadDaemon(ex)
}
