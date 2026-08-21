package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	LocalPort int               `json:"local_port,omitempty"`
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
func (pf *ProfileFile) Save() error {
	path, err := ConfigFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
