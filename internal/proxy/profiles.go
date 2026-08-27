package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultGlobalNoProxy lists hosts that bypass the proxy regardless of which
// profile (if any) is active, unless overridden via "proxy config set".
var DefaultGlobalNoProxy = []string{"host.docker.internal", "localhost", "127.0.0.1"}

// CurrentProfileName is the reserved profile that holds an ad-hoc config
// applied by "proxy set --via-local". The daemon only ever reads
// active_profile, so a one-off set needs somewhere to live.
const CurrentProfileName = "_current"

// DefaultLocalPort is where the local daemon listens unless the user picked
// another port with "proxy serve install --port".
const DefaultLocalPort = 8888

// ProfileFile is the on-disk format for saved proxy profiles.
type ProfileFile struct {
	ActiveProfile string   `json:"active_profile,omitempty"`
	GlobalNoProxy []string `json:"global_no_proxy,omitempty"`
	// LastProfile is what "proxy on" restores after "proxy off".
	LastProfile string `json:"last_profile,omitempty"`
	// DockerBridge records that the daemon also listens on the Docker bridge,
	// which is what lets build containers reach it. Off by default: it exposes
	// the proxy to every container on the machine.
	DockerBridge bool `json:"docker_bridge,omitempty"`
	// ViaLocal records that the targets point at the local daemon, so
	// "proxy status" can warn when the daemon is not running.
	ViaLocal bool `json:"via_local,omitempty"`
	// LocalPort is the port the daemon listens on. It is written by
	// "proxy serve install" so that pointing targets at the loopback and
	// reporting the daemon's address never guess a port the daemon does
	// not actually use. Zero means DefaultLocalPort.
	LocalPort int `json:"local_port,omitempty"`
	// CloseToTray records the GUI-only preference that closing the main
	// window hides it in the tray indicator instead of quitting. It lives
	// here (rather than a second config file) because a second file would
	// duplicate locking, atomic-write and path-resolution logic for one
	// boolean. The CLI never reads or writes this field.
	CloseToTray bool `json:"close_to_tray,omitempty"`
	// LogsSince is the cut-off "proxy logs" and the GUI's Daemon page read
	// from, as an RFC3339 timestamp. It is what "clearing the logs" means
	// here, and the name is deliberate: nothing is deleted.
	//
	// The journal cannot delete one unit's entries — journalctl's
	// --vacuum-* flags operate on journal FILES and ignore -u, so a real
	// delete would take every user unit's logs with it. Recording where to
	// start reading gets the user what they asked for (an empty table, a
	// fresh start) without destroying anyone else's data, and it is
	// reversible: "proxy logs --all" ignores it, and clearing the field
	// brings everything back.
	LogsSince string            `json:"logs_since,omitempty"`
	Profiles  map[string]Config `json:"profiles"`
}

// EffectiveLocalPort returns the configured daemon port, or the default when
// none was ever recorded.
func (pf *ProfileFile) EffectiveLocalPort() int {
	if pf.LocalPort > 0 {
		return pf.LocalPort
	}
	return DefaultLocalPort
}

// EffectiveGlobalNoProxy returns the configured global no-proxy list,
// falling back to DefaultGlobalNoProxy when none has been set.
func (pf *ProfileFile) EffectiveGlobalNoProxy() []string {
	if pf.GlobalNoProxy != nil {
		return pf.GlobalNoProxy
	}
	return DefaultGlobalNoProxy
}

// ConfigFilePath returns the path to the profiles config file. It does not
// check whether the file or its parent directory exist.
func ConfigFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return filepath.Join(dir, "proxy-helper", "config.json"), nil
}

// LoadProfiles reads the profiles config file. A missing file is not an
// error; it returns an empty ProfileFile ready to be populated and saved.
func LoadProfiles() (*ProfileFile, error) {
	path, err := ConfigFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ProfileFile{Profiles: map[string]Config{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var pf ProfileFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if pf.Profiles == nil {
		pf.Profiles = map[string]Config{}
	}
	return &pf, nil
}

// Get returns the named profile, if any.
func (pf *ProfileFile) Get(name string) (Config, bool) {
	cfg, ok := pf.Profiles[name]
	return cfg, ok
}

// Off clears the active profile, which makes the daemon route everything
// direct. It remembers what was active so On can restore it. Calling Off
// when already off keeps the remembered profile.
func (pf *ProfileFile) Off() {
	if pf.ActiveProfile != "" {
		pf.LastProfile = pf.ActiveProfile
	}
	pf.ActiveProfile = ""
}

// On activates name, or the last profile that was active when name is empty.
func (pf *ProfileFile) On(name string) error {
	if name == "" {
		name = pf.LastProfile
	}
	if name == "" {
		return fmt.Errorf("no previously active profile to restore; run \"proxy on <profile>\" (see \"proxy profile list\")")
	}
	if _, ok := pf.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
	}
	pf.ActiveProfile = name
	return nil
}

// SetCurrent stores an ad-hoc config in the reserved profile and activates it.
func (pf *ProfileFile) SetCurrent(cfg Config) {
	if pf.Profiles == nil {
		pf.Profiles = map[string]Config{}
	}
	pf.Profiles[CurrentProfileName] = cfg
	pf.ActiveProfile = CurrentProfileName
}

// Save writes the profiles config file, creating its parent directory if
// needed. It uses 0600/0700 permissions since profiles may hold proxy
// credentials in plain text.
//
// The write is atomic: it writes to a temp file in the same directory (so
// the final rename stays on one filesystem) and renames it over the target.
// A rename within a directory is atomic, so a concurrent reader — which
// never takes the file lock, only writers do — sees either the old file or
// the new one in full, never a torn write from a truncating WriteFile.
func (pf *ProfileFile) Save() error {
	path, err := ConfigFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".config.json.tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	// The temp file must never be readable by anyone else, even briefly:
	// this config can hold proxy credentials in plain text, and
	// os.CreateTemp's default mode is 0600 already, but chmod explicitly so
	// the guarantee does not depend on that default staying unchanged.
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting permissions on temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file for %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// withProfileFileLock acquires an exclusive lock on the config file's
// sibling .lock file and runs fn while holding it. The lock lives in a
// sibling file rather than in config.json itself: Save rewrites the config,
// and a lock held on a descriptor that is about to be replaced protects
// nothing.
func withProfileFileLock(fn func() error) error {
	path, err := ConfigFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	return fn()
}

// WithProfileLock runs fn against the profile file while holding an
// exclusive lock, so a read-modify-write cycle cannot lose an update to a
// concurrent one. The GUI and a CLI run can be active at the same time.
func WithProfileLock(fn func(*ProfileFile) error) error {
	return withProfileFileLock(func() error {
		pf, err := LoadProfiles()
		if err != nil {
			return err
		}
		if err := fn(pf); err != nil {
			return err
		}
		return pf.Save()
	})
}

// SaveLocked writes pf to disk while holding the same exclusive lock as
// WithProfileLock, without reloading it from disk first. Use this when the
// caller already holds a *ProfileFile it must not discard by re-reading the
// file — for example ApplyViaLocal, whose caller has already set
// ActiveProfile on pf and must not have that choice silently dropped.
//
// This gives a narrower guarantee than WithProfileLock: the read that
// produced pf happened outside the lock, so SaveLocked only prevents a torn
// write, not a lost update — a concurrent writer's change made between that
// read and this call is overwritten by whatever pf holds. That is inherent
// to accepting an already-loaded ProfileFile rather than a defect; a caller
// that needs the full read-modify-write guarantee should use
// WithProfileLock instead.
func SaveLocked(pf *ProfileFile) error {
	return withProfileFileLock(pf.Save)
}
