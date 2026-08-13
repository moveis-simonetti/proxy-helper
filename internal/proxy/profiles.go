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

// ProfileFile is the on-disk format for saved proxy profiles.
type ProfileFile struct {
	ActiveProfile string            `json:"active_profile,omitempty"`
	GlobalNoProxy []string          `json:"global_no_proxy,omitempty"`
	Profiles      map[string]Config `json:"profiles"`
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
