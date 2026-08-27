package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// gnomeTarget manages the desktop-wide proxy via gsettings, used by GNOME
// and anything reading org.gnome.system.proxy (e.g. GTK apps).
type gnomeTarget struct{}

func NewGnomeTarget() Target { return &gnomeTarget{} }

func (t *gnomeTarget) Name() string { return "gnome" }

// RequiresRoot reflects that Unset needs sudo only on systems where the
// PackageKit cache workaround below actually applies; Set never needs it.
func (t *gnomeTarget) RequiresRoot() bool {
	_, err := os.Stat(packageKitDBPath)
	return err == nil
}

// SessionScoped is true: gsettings talks to the invoking user's own dconf
// database over their session D-Bus. Run as root (e.g. under pkexec, which
// has no $DISPLAY and no session D-Bus) it cannot reach that session at
// all, so gsettings writes fail — a caller choosing whether to elevate must
// check this before RequiresRoot, and never elevate this target.
func (t *gnomeTarget) SessionScoped() bool { return true }

func (t *gnomeTarget) Available() bool {
	if !commandExists("gsettings") {
		return false
	}
	out, err := exec.Command("gsettings", "list-schemas").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "org.gnome.system.proxy")
}

func (t *gnomeTarget) Set(ex *Executor, cfg Config) error {
	schema := "org.gnome.system.proxy.http"
	if cfg.Scheme == "socks5" {
		schema = "org.gnome.system.proxy.socks"
	}

	if err := ex.Run("gsettings", "set", "org.gnome.system.proxy", "mode", "manual"); err != nil {
		return err
	}
	// gsettings exits 0 even when dconf could not commit the change (e.g.
	// no session D-Bus reachable) — the failure only shows up as a warning
	// on stderr, never in the exit code, so an exit-code check alone
	// reports success for a write that did nothing. Reading the mode back
	// catches that: every key below shares the same dconf backend, so if
	// this one write took effect the rest did too.
	if err := verifyGsettingsWrite(ex, "org.gnome.system.proxy", "mode", "manual"); err != nil {
		return err
	}
	if err := ex.Run("gsettings", "set", schema, "host", cfg.Host); err != nil {
		return err
	}
	port := cfg.Port
	if port == "" {
		port = "0"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("gnome target requires a numeric port, got %q", port)
	}
	if err := ex.Run("gsettings", "set", schema, "port", port); err != nil {
		return err
	}

	// Mirror the same host/port on the https schema too, unless we're using socks.
	if schema != "org.gnome.system.proxy.https" {
		if err := ex.Run("gsettings", "set", "org.gnome.system.proxy.https", "host", cfg.Host); err != nil {
			return err
		}
		if err := ex.Run("gsettings", "set", "org.gnome.system.proxy.https", "port", port); err != nil {
			return err
		}
	}

	// Authentication only exists on the http schema — org.gnome.system.
	// proxy.socks has no authentication-user/use-authentication keys — so
	// this always targets org.gnome.system.proxy.http regardless of which
	// schema is active for host/port above.
	//
	// The else branch matters as much as the if: without it, a config with
	// no username (e.g. the local daemon's credential-free loopback URL)
	// left whatever an earlier profile had written — use-authentication
	// stuck true and a stale authentication-user — untouched, so GNOME
	// went on offering a dead username to a proxy that never asked for
	// one. Every Set must make the written state match cfg exactly, not
	// only add to it.
	if cfg.Username != "" && schema == "org.gnome.system.proxy.http" {
		if err := ex.Run("gsettings", "set", schema, "use-authentication", "true"); err != nil {
			return err
		}
		if err := ex.Run("gsettings", "set", schema, "authentication-user", cfg.Username); err != nil {
			return err
		}
		if err := ex.Run("gsettings", "set", schema, "authentication-password", cfg.Password); err != nil {
			return err
		}
	} else {
		authSchema := "org.gnome.system.proxy.http"
		if err := ex.Run("gsettings", "set", authSchema, "use-authentication", "false"); err != nil {
			return err
		}
		// Verified explicitly, like the mode write above: this is the key
		// that actually stops GNOME offering a stale username, so a
		// silently-failed dconf write here (see verifyGsettingsWrite's doc
		// comment) must not be reported as success.
		if err := verifyGsettingsWrite(ex, authSchema, "use-authentication", "false"); err != nil {
			return err
		}
		if err := ex.Run("gsettings", "set", authSchema, "authentication-user", ""); err != nil {
			return err
		}
		if err := ex.Run("gsettings", "set", authSchema, "authentication-password", ""); err != nil {
			return err
		}
	}

	if len(cfg.NoProxy) > 0 {
		list := gsettingsStringList(cfg.NoProxy)
		if err := ex.Run("gsettings", "set", "org.gnome.system.proxy", "ignore-hosts", list); err != nil {
			return err
		}
	}

	return nil
}

func (t *gnomeTarget) Unset(ex *Executor) error {
	if err := ex.Run("gsettings", "set", "org.gnome.system.proxy", "mode", "none"); err != nil {
		return err
	}
	if err := verifyGsettingsWrite(ex, "org.gnome.system.proxy", "mode", "none"); err != nil {
		return err
	}
	// Same reasoning as Set's clearing branch: mode=none stops GNOME from
	// using the proxy, but leaves whatever credentials a previous Set
	// wrote sitting in dconf. Clear them so a later "gsettings get" (or
	// another app reading this schema directly) doesn't see a stale
	// username and use-authentication=true for a proxy that is off.
	authSchema := "org.gnome.system.proxy.http"
	if err := ex.Run("gsettings", "set", authSchema, "use-authentication", "false"); err != nil {
		return err
	}
	if err := verifyGsettingsWrite(ex, authSchema, "use-authentication", "false"); err != nil {
		return err
	}
	if err := ex.Run("gsettings", "set", authSchema, "authentication-user", ""); err != nil {
		return err
	}
	if err := ex.Run("gsettings", "set", authSchema, "authentication-password", ""); err != nil {
		return err
	}

	return clearPackageKitProxyCache(ex)
}

// verifyGsettingsWrite reads schema/key back and confirms it matches want,
// so a caller can tell a write that silently failed (see Set's comment)
// from one that actually took effect. Skipped in dry-run: a DryRun
// Executor never actually ran the "set", so there is nothing to read back
// yet, and reading real dconf state would just show the value from before
// this (simulated) change.
func verifyGsettingsWrite(ex *Executor, schema, key, want string) error {
	if ex.DryRun {
		return nil
	}
	out, err := ex.RunOutput("gsettings", "get", schema, key)
	if err != nil {
		return fmt.Errorf("verifying gsettings %s %s: %w", schema, key, err)
	}
	got := strings.Trim(strings.TrimSpace(string(out)), "'")
	if got != want {
		return fmt.Errorf("gsettings set %s %s did not take effect (read back %q, expected %q) — dconf may be unreachable (no session D-Bus?)", schema, key, got, want)
	}
	return nil
}

const packageKitDBPath = "/var/lib/PackageKit/transactions.db"

// clearPackageKitProxyCache works around PackageKit/PackageKit#392:
// PackageKit (used by GNOME Software, Discover, etc.) caches the proxy it
// last saw from GSettings in its own sqlite database and doesn't clear it
// when the desktop proxy is turned off, so package managers built on it
// keep using a stale proxy. Deleting the cached row and restarting the
// daemon is the documented workaround. A no-op where PackageKit isn't in use.
func clearPackageKitProxyCache(ex *Executor) error {
	if _, err := os.Stat(packageKitDBPath); err != nil {
		return nil
	}
	if !commandExists("sqlite3") || !commandExists("pkcon") {
		return nil
	}
	// This workaround needs root, but gnome is session-scoped and so must
	// never be routed into an elevated call (see SessionScoped's doc
	// comment) — it always runs in-process, as the invoking user. When
	// that user process has no way to gain root (EscalateNone, the GUI's
	// in-process executor) and isn't root already, RunPrivileged below
	// would either fail outright or, for the CLI's default EscalateSudo,
	// block on a terminal prompt that may not exist. Skip the workaround
	// with a warning instead of failing the whole Unset over what is only
	// a PackageKit cache staleness issue: the actual proxy setting (mode)
	// was already cleared and verified above.
	if !IsRoot() && ex.Escalation == EscalateNone {
		ex.Warn("skipping PackageKit proxy cache clear: this process cannot gain root here; run \"proxy unset --targets gnome\" from a shell that can (e.g. with sudo) to clear it")
		return nil
	}
	if err := ex.RunPrivileged("sqlite3", packageKitDBPath, "DELETE FROM proxy;"); err != nil {
		return fmt.Errorf("clearing PackageKit proxy cache: %w", err)
	}
	return ex.RunPrivileged("pkcon", "quit")
}

func (t *gnomeTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "gsettings/org.gnome.system.proxy schema not found"
		return st, nil
	}

	mode, err := exec.Command("gsettings", "get", "org.gnome.system.proxy", "mode").Output()
	if err != nil {
		st.Detail = fmt.Sprintf("error: %v", err)
		return st, nil
	}
	modeStr := strings.Trim(strings.TrimSpace(string(mode)), "'")
	st.Enabled = modeStr == "manual"
	if !st.Enabled {
		st.Detail = "not set (mode=" + modeStr + ")"
		return st, nil
	}

	cfg := Config{
		Host: gsettingsGetString("org.gnome.system.proxy.http", "host"),
		Port: gsettingsGetString("org.gnome.system.proxy.http", "port"),
	}
	if gsettingsGetString("org.gnome.system.proxy.http", "use-authentication") == "true" {
		cfg.Username = gsettingsGetString("org.gnome.system.proxy.http", "authentication-user")
		cfg.Password = gsettingsGetString("org.gnome.system.proxy.http", "authentication-password")
	}

	url, err := cfg.URL()
	if err != nil {
		st.Detail = fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
		return st, nil
	}
	st.Detail = redactSecrets(url)
	return st, nil
}

// gsettingsGetString runs `gsettings get <schema> <key>` and strips the
// surrounding quotes gsettings prints around string values.
func gsettingsGetString(schema, key string) string {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(out)), "'")
}

func gsettingsStringList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = "'" + strings.ReplaceAll(item, "'", "\\'") + "'"
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
