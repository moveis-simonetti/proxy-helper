package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// vscodeTarget configures the proxy for VS Code and editors that fork its
// user settings.json format (Cursor, Antigravity, ...).
type vscodeTarget struct{}

func NewVscodeTarget() Target { return &vscodeTarget{} }

func (t *vscodeTarget) Name() string       { return "vscode" }
func (t *vscodeTarget) RequiresRoot() bool { return false }

func (t *vscodeTarget) SessionScoped() bool { return false }
func (t *vscodeTarget) Available() bool     { return true }

type vscodeProduct struct {
	dir  string // config dir name under $XDG_CONFIG_HOME/<dir>/User/settings.json
	name string
	cmd  string // CLI binary, used to spot an install that has no settings.json
	snap string // snap package name, empty when the editor ships no snap
}

// vscodeProducts lists the editors sharing VS Code's settings.json format.
var vscodeProducts = []vscodeProduct{
	{"Code", "VS Code", "code", "code"},
	{"Cursor", "Cursor", "cursor", ""},
	{"Antigravity", "Antigravity", "antigravity", ""},
}

// vscodeInstall is one editor installation on this machine: the label the
// status line shows and the settings.json that belongs to it.
//
// Installation, not product, is the unit of work here because one editor can
// be present twice in different packagings, each with its own settings.json.
// A snap is confined to ~/snap/<pkg>/current and never reads the host's
// ~/.config, so writing the proxy to the "one true path" configures a file
// the editor the user launches may never open.
type vscodeInstall struct {
	name string
	path string
}

// vscodeSettingsPath is where a natively packaged editor (.deb, tarball,
// AppImage) keeps its settings.
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

// isSnapBinary reports whether a resolved PATH entry belongs to a snap.
//
// The symlink is deliberately not followed: /snap/bin/<cmd> points at the
// snap wrapper (/usr/bin/snap), so resolving it loses exactly the fact being
// tested.
func isSnapBinary(path string) bool {
	return strings.HasPrefix(path, "/snap/")
}

// snapSettingsPath is where a snap-confined editor keeps its settings.
// XDG_CONFIG_HOME is intentionally ignored: the snap runs with its own HOME
// and would not honour the host's value.
func snapSettingsPath(snapPkg, dir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "snap", snapPkg, "current", ".config", dir, "User", "settings.json"), nil
}

// candidates lists every place this product could keep its settings, native
// packaging first.
func (p vscodeProduct) candidates() []vscodeInstall {
	var out []vscodeInstall
	if path, err := vscodeSettingsPath(p.dir); err == nil {
		out = append(out, vscodeInstall{p.name, path})
	}
	if p.snap != "" {
		if path, err := snapSettingsPath(p.snap, p.dir); err == nil {
			out = append(out, vscodeInstall{p.name + " (snap)", path})
		}
	}
	return out
}

// exists reports whether this installation is really on the machine.
//
// Either the settings.json or the User directory containing it will do: the
// editors write settings.json only once a setting is changed, so a working
// install can go a long time without one — and treating "no file" as "no
// editor" is what made Set skip these users silently. The User directory,
// not the product directory above it, because that one survives an uninstall
// as a leftover.
func (i vscodeInstall) exists() bool {
	if _, err := os.Stat(i.path); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Dir(i.path))
	return err == nil
}

// vscodeInstalls returns every editor installation found on this machine.
//
// A product with no directories at all still counts when its CLI is on PATH
// — an install that has never been launched. Which candidate that resolves
// to depends on the packaging: a /snap/bin CLI means the snap location, and
// pointing it at ~/.config instead would write somewhere the confined editor
// cannot read.
func vscodeInstalls() []vscodeInstall {
	var found []vscodeInstall
	for _, p := range vscodeProducts {
		candidates := p.candidates()

		var present []vscodeInstall
		for _, c := range candidates {
			if c.exists() {
				present = append(present, c)
			}
		}
		if len(present) > 0 {
			found = append(found, present...)
			continue
		}

		binary, err := exec.LookPath(p.cmd)
		if err != nil {
			continue
		}
		wantSnap := isSnapBinary(binary)
		for _, c := range candidates {
			if strings.HasSuffix(c.name, " (snap)") == wantSnap {
				found = append(found, c)
				break
			}
		}
	}
	return found
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

	for _, inst := range vscodeInstalls() {
		raw, _, err := readSettings(inst.path)
		switch {
		case os.IsNotExist(err):
			// The editor is here, it just never wrote a settings.json.
			// Start from an empty document; Executor.WriteFile creates
			// the User directory when it is missing too.
			raw = []byte("{}\n")
		case err != nil:
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
				return fmt.Errorf("%s: %w", inst.name, err)
			}
		}
		if len(cfg.NoProxy) > 0 {
			encoded, err := encodeJSON(cfg.NoProxy)
			if err != nil {
				return err
			}
			if raw, err = setJSONCKey(raw, "http.noProxy", encoded); err != nil {
				return fmt.Errorf("%s: %w", inst.name, err)
			}
		} else if raw, err = removeJSONCKeys(raw, "http.noProxy"); err != nil {
			return fmt.Errorf("%s: %w", inst.name, err)
		}

		if err := ex.WriteFile(inst.path, raw, 0o644); err != nil {
			return fmt.Errorf("%s: %w", inst.name, err)
		}
	}
	return nil
}

func (t *vscodeTarget) Unset(ex *Executor) error {
	for _, inst := range vscodeInstalls() {
		raw, doc, err := readSettings(inst.path)
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
			return fmt.Errorf("%s: %w", inst.name, err)
		}
		if err := ex.WriteFile(inst.path, raw, 0o644); err != nil {
			return fmt.Errorf("%s: %w", inst.name, err)
		}
	}
	return nil
}

func (t *vscodeTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: true}

	var found bool
	var enabled []string
	for _, inst := range vscodeInstalls() {
		// Reaching here already means the editor was found; a missing
		// settings.json only means it has no proxy set.
		found = true
		_, doc, err := readSettings(inst.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return st, err
		}
		if proxyURL, ok := doc["http.proxy"].(string); ok && proxyURL != "" {
			enabled = append(enabled, fmt.Sprintf("%s=%s", inst.name, redactSecrets(proxyURL)))
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
