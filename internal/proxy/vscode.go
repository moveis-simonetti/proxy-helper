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

// readJSONObject parses a settings.json file. It only understands strict
// JSON (like the "jq"-based reference implementation this mirrors), so
// comments/trailing commas some editors tolerate in this file are not
// preserved across a round trip.
func readJSONObject(path string) (map[string]interface{}, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return map[string]interface{}{}, nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return doc, nil
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
		doc, err := readJSONObject(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}

		doc["http.proxy"] = proxyURL
		doc["http.proxyStrictSSL"] = false
		if len(cfg.NoProxy) > 0 {
			doc["http.noProxy"] = cfg.NoProxy
		} else {
			delete(doc, "http.noProxy")
		}

		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		if err := ex.WriteFile(path, append(out, '\n'), 0o644); err != nil {
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
		doc, err := readJSONObject(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if _, ok := doc["http.proxy"]; !ok {
			continue
		}

		delete(doc, "http.proxy")
		delete(doc, "http.proxyStrictSSL")
		delete(doc, "http.noProxy")

		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		if err := ex.WriteFile(path, append(out, '\n'), 0o644); err != nil {
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
		doc, err := readJSONObject(path)
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
