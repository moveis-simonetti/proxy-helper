package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// vscodeTarget configures the proxy for VS Code and editors that fork its
// user settings.json format (Cursor, Antigravity, ...).
type vscodeTarget struct{}

func NewVscodeTarget() Target { return &vscodeTarget{} }

func (t *vscodeTarget) Name() string       { return "vscode" }
func (t *vscodeTarget) RequiresRoot() bool { return false }
func (t *vscodeTarget) Available() bool    { return true }

type vscodeProduct struct {
	dir  string // config dir name under $XDG_CONFIG_HOME/<dir>/User/settings.json
	name string
}

// vscodeProducts lists the editors sharing VS Code's settings.json format.
// Editors not installed (no settings.json yet) are silently skipped.
var vscodeProducts = []vscodeProduct{
	{"Code", "VS Code"},
	{"Cursor", "Cursor"},
	{"Antigravity", "Antigravity"},
}

func vscodeSettingsPath(dir string) (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, dir, "User", "settings.json"), nil
}

// readSettings returns a settings.json file both as raw bytes (for in-place
// editing) and parsed (for reading values). Editors in this family write JSON
// with comments, and users hand-edit these files, so comments are tolerated
// when parsing and preserved when writing.
func readSettings(path string) (raw []byte, doc map[string]interface{}, err error) {
	raw, err = os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return []byte("{}\n"), map[string]interface{}{}, nil
	}
	if err := json.Unmarshal(stripJSONC(raw), &doc); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return raw, doc, nil
}

// encodeJSON renders a value the way it should appear inside settings.json.
func encodeJSON(v interface{}) (string, error) {
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (t *vscodeTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}

	for _, p := range vscodeProducts {
		path, err := vscodeSettingsPath(p.dir)
		if err != nil {
			return err
		}
		raw, _, err := readSettings(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}

		// Edit only the proxy keys: the user's comments, key order and
		// formatting elsewhere in the file must survive untouched.
		values := []struct {
			key string
			val interface{}
		}{
			{"http.proxy", proxyURL},
			{"http.proxyStrictSSL", false},
		}
		for _, kv := range values {
			encoded, err := encodeJSON(kv.val)
			if err != nil {
				return err
			}
			if raw, err = setJSONCKey(raw, kv.key, encoded); err != nil {
				return fmt.Errorf("%s: %w", p.name, err)
			}
		}
		if len(cfg.NoProxy) > 0 {
			encoded, err := encodeJSON(cfg.NoProxy)
			if err != nil {
				return err
			}
			if raw, err = setJSONCKey(raw, "http.noProxy", encoded); err != nil {
				return fmt.Errorf("%s: %w", p.name, err)
			}
		} else if raw, err = removeJSONCKeys(raw, "http.noProxy"); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}

		if err := ex.WriteFile(path, raw, 0o644); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	return nil
}

func (t *vscodeTarget) Unset(ex *Executor) error {
	for _, p := range vscodeProducts {
		path, err := vscodeSettingsPath(p.dir)
		if err != nil {
			return err
		}
		raw, doc, err := readSettings(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if _, ok := doc["http.proxy"]; !ok {
			continue
		}

		raw, err = removeJSONCKeys(raw, "http.proxy", "http.proxyStrictSSL", "http.noProxy")
		if err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
		if err := ex.WriteFile(path, raw, 0o644); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	return nil
}

func (t *vscodeTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: true}

	var found bool
	var enabled []string
	for _, p := range vscodeProducts {
		path, err := vscodeSettingsPath(p.dir)
		if err != nil {
			return st, err
		}
		_, doc, err := readSettings(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return st, err
		}
		found = true
		if proxyURL, ok := doc["http.proxy"].(string); ok && proxyURL != "" {
			enabled = append(enabled, fmt.Sprintf("%s=%s", p.name, redactSecrets(proxyURL)))
		}
	}

	if !found {
		st.Detail = "no VS Code-family editor found"
		return st, nil
	}
	if len(enabled) == 0 {
		st.Detail = "not set"
		return st, nil
	}
	st.Enabled = true
	st.Detail = strings.Join(enabled, ", ")
	return st, nil
}
