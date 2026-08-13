package proxy

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// gnomeTarget manages the desktop-wide proxy via gsettings, used by GNOME
// and anything reading org.gnome.system.proxy (e.g. GTK apps).
type gnomeTarget struct{}

func NewGnomeTarget() Target { return &gnomeTarget{} }

func (t *gnomeTarget) Name() string       { return "gnome" }
func (t *gnomeTarget) RequiresRoot() bool { return false }

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
	return ex.Run("gsettings", "set", "org.gnome.system.proxy", "mode", "none")
}

func (t *gnomeTarget) Status(elevate bool) (Status, error) {
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
