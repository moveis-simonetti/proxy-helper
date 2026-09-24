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
	dir     string // config dir name, e.g. "Code" in .../<dir>/User/settings.json
	name    string
	cmd     string // native CLI binary name
	snap    string // snap package name, empty when the editor ships no snap
	flatpak string // flatpak application id, empty when there is no flatpak
}

// vscodeProducts lists the editors sharing VS Code's settings.json format.
var vscodeProducts = []vscodeProduct{
	{dir: "Code", name: "VS Code", cmd: "code", snap: "code", flatpak: "com.visualstudio.code"},
	{dir: "Cursor", name: "Cursor", cmd: "cursor"},
	{dir: "Antigravity", name: "Antigravity", cmd: "antigravity"},
}

// packaging is how an editor was installed. It decides where settings.json
// lives and how to tell the editor is present — a snap and a flatpak each
// keep their config inside a sandboxed home that never sees the host's
// ~/.config, so "the one true path" would configure a file the editor the
// user actually launches never opens.
type packaging int

const (
	pkgNative packaging = iota
	pkgSnap
	pkgFlatpak
)

func (p packaging) label() string {
	switch p {
	case pkgSnap:
		return " (snap)"
	case pkgFlatpak:
		return " (flatpak)"
	default:
		return ""
	}
}

// vscodeInstall is one editor installation: the label the status line shows,
// the settings.json that belongs to it, and enough of the product to decide
// whether it is really present.
//
// Installation, not product, is the unit of work because one editor can be
// present several times in different packagings, each with its own
// settings.json — configuring one and missing another leaves whichever the
// user launches unproxied.
type vscodeInstall struct {
	name string
	path string
	pkg  packaging
	prod vscodeProduct
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

// flatpakSettingsPath is where a flatpak-confined editor keeps its settings:
// the sandbox maps ~/.var/app/<id>/config onto its $XDG_CONFIG_HOME, and the
// product dir inside is the same one the native packaging uses.
func flatpakSettingsPath(appID, dir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".var", "app", appID, "config", dir, "User", "settings.json"), nil
}

// flatpakInstalled reports whether a flatpak app is installed, by the
// wrapper flatpak drops at install time — before first launch, so before
// ~/.var/app/<id> exists. Both the per-user and system-wide export dirs are
// checked; shelling out to `flatpak info` would be heavier for the same
// answer.
func flatpakInstalled(appID string) bool {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	for _, base := range []string{
		filepath.Join(dataHome, "flatpak", "exports", "bin"),
		"/var/lib/flatpak/exports/bin",
	} {
		if base == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, appID)); err == nil {
			return true
		}
	}
	return false
}

// snapInstalled reports whether a snap package's confined home exists.
func snapInstalled(snapPkg string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, "snap", snapPkg))
	return err == nil
}

// candidates lists every place this product could keep its settings, native
// packaging first.
func (p vscodeProduct) candidates() []vscodeInstall {
	var out []vscodeInstall
	add := func(pkg packaging, path string, err error) {
		if err == nil {
			out = append(out, vscodeInstall{p.name + pkg.label(), path, pkg, p})
		}
	}
	path, err := vscodeSettingsPath(p.dir)
	add(pkgNative, path, err)
	if p.snap != "" {
		path, err := snapSettingsPath(p.snap, p.dir)
		add(pkgSnap, path, err)
	}
	if p.flatpak != "" {
		path, err := flatpakSettingsPath(p.flatpak, p.dir)
		add(pkgFlatpak, path, err)
	}
	return out
}

// exists reports whether this installation is really on the machine.
//
// The settings.json or the User directory containing it is the everyday
// signal: these editors write settings.json only once a setting is changed,
// so a working install can go a long time without one — and treating "no
// file" as "no editor" is what made Set skip these users silently. The User
// directory, not the product directory above it, because that one survives
// an uninstall as a leftover.
//
// Failing that, a packaging-specific install-time marker catches an editor
// that has never been launched, so none of its directories exist yet.
func (i vscodeInstall) exists() bool {
	if _, err := os.Stat(i.path); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Dir(i.path)); err == nil {
		return true
	}
	switch i.pkg {
	case pkgNative:
		// A resolved /snap/bin/code belongs to the snap candidate, not
		// this one — the symlink is not followed on purpose, it points
		// at the snap wrapper and resolving it loses that fact.
		bin, err := exec.LookPath(i.prod.cmd)
		return err == nil && !isSnapBinary(bin)
	case pkgSnap:
		if snapInstalled(i.prod.snap) {
			return true
		}
		bin, err := exec.LookPath(i.prod.cmd)
		return err == nil && isSnapBinary(bin)
	case pkgFlatpak:
		return flatpakInstalled(i.prod.flatpak)
	}
	return false
}

// vscodeInstalls returns every editor installation found on this machine —
// every candidate location that a real install backs.
func vscodeInstalls() []vscodeInstall {
	var found []vscodeInstall
	for _, p := range vscodeProducts {
		for _, c := range p.candidates() {
			if c.exists() {
				found = append(found, c)
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
		// preview accumulates just the keys being written. settings.json is
		// the user's, and extensions keep API keys in it — echoing the
		// merged file would print them for anyone who merely asked what the
		// command would do.
		var preview []string
		for _, kv := range values {
			encoded, err := encodeJSON(kv.val)
			if err != nil {
				return err
			}
			if raw, err = setJSONCKey(raw, kv.key, encoded); err != nil {
				return fmt.Errorf("%s: %w", inst.name, err)
			}
			preview = append(preview, fmt.Sprintf("%q: %s", kv.key, encoded))
		}
		if len(cfg.NoProxy) > 0 {
			encoded, err := encodeJSON(cfg.NoProxy)
			if err != nil {
				return err
			}
			if raw, err = setJSONCKey(raw, "http.noProxy", encoded); err != nil {
				return fmt.Errorf("%s: %w", inst.name, err)
			}
			preview = append(preview, fmt.Sprintf("%q: %s", "http.noProxy", encoded))
		} else if raw, err = removeJSONCKeys(raw, "http.noProxy"); err != nil {
			return fmt.Errorf("%s: %w", inst.name, err)
		}

		if err := ex.WriteFilePreview(inst.path, raw, strings.Join(preview, "\n"), 0o644); err != nil {
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
		if err := ex.WriteFilePreview(inst.path, raw,
			"(removing http.proxy, http.proxyStrictSSL, http.noProxy)", 0o644); err != nil {
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
