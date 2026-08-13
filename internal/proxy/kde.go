package proxy

import (
	"fmt"
	"os/exec"
	"strings"
)

// kdeTarget configures the KIO (KDE I/O) proxy settings used by KDE Plasma
// and complying applications, via kwriteconfig<major-version>.
type kdeTarget struct{}

func NewKdeTarget() Target { return &kdeTarget{} }

func (t *kdeTarget) Name() string       { return "kde" }
func (t *kdeTarget) RequiresRoot() bool { return false }

// kdeMajorVersion returns the installed KDE Plasma major version (e.g.
// "5", "6"), or "" if plasmashell isn't installed or its version can't be
// parsed. kwriteconfig/kreadconfig are versioned by this number.
func kdeMajorVersion() string {
	if !commandExists("plasmashell") {
		return ""
	}
	out, err := exec.Command("plasmashell", "--version").Output()
	if err != nil {
		return ""
	}
	// e.g. "plasmashell 5.24.5" -> "5"
	version := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "plasmashell"))
	major, _, _ := strings.Cut(version, ".")
	return major
}

func (t *kdeTarget) kwriteconfig() string {
	major := kdeMajorVersion()
	if major == "" {
		return ""
	}
	return "kwriteconfig" + major
}

func (t *kdeTarget) kreadconfig() string {
	major := kdeMajorVersion()
	if major == "" {
		return ""
	}
	return "kreadconfig" + major
}

func (t *kdeTarget) Available() bool {
	kw := t.kwriteconfig()
	return kw != "" && commandExists(kw)
}

// kioNoProxyString converts wildcard-subdomain hosts (*.example.com) to
// KIO's leading-dot form (.example.com) and joins them for NoProxyFor.
func kioNoProxyString(hosts []string) string {
	stripped := make([]string, len(hosts))
	for i, h := range hosts {
		stripped[i] = strings.TrimPrefix(h, "*")
	}
	return strings.Join(stripped, ",")
}

func (t *kdeTarget) Set(ex *Executor, cfg Config) error {
	if cfg.Host == "" {
		return fmt.Errorf("proxy host is required")
	}
	kw := t.kwriteconfig()
	if kw == "" {
		return fmt.Errorf("kwriteconfig not found (is KDE Plasma installed?)")
	}

	hostPort := cfg.Host
	if cfg.Port != "" {
		hostPort = cfg.Host + " " + cfg.Port
	}

	set := func(key, value string) error {
		return ex.Run(kw, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key, value)
	}

	if err := set("ProxyType", "1"); err != nil {
		return err
	}
	if err := set("httpProxy", hostPort); err != nil {
		return err
	}
	if err := set("httpsProxy", hostPort); err != nil {
		return err
	}
	if len(cfg.NoProxy) > 0 {
		if err := set("NoProxyFor", kioNoProxyString(cfg.NoProxy)); err != nil {
			return err
		}
	}
	return nil
}

func (t *kdeTarget) Unset(ex *Executor) error {
	kw := t.kwriteconfig()
	if kw == "" {
		return fmt.Errorf("kwriteconfig not found (is KDE Plasma installed?)")
	}
	return ex.Run(kw, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType", "0")
}

func (t *kdeTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "kwriteconfig/plasmashell not found"
		return st, nil
	}

	kr := t.kreadconfig()
	if kr == "" || !commandExists(kr) {
		st.Detail = "kreadconfig not found"
		return st, nil
	}

	typeOut, err := exec.Command(kr, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "ProxyType").Output()
	if err != nil {
		st.Detail = fmt.Sprintf("error: %v", err)
		return st, nil
	}
	proxyType := strings.TrimSpace(string(typeOut))
	st.Enabled = proxyType == "1"
	if !st.Enabled {
		st.Detail = "not set (ProxyType=" + proxyType + ")"
		return st, nil
	}

	httpProxy, err := exec.Command(kr, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", "httpProxy").Output()
	if err != nil {
		st.Detail = fmt.Sprintf("error: %v", err)
		return st, nil
	}
	st.Detail = redactSecrets(strings.TrimSpace(string(httpProxy)))
	return st, nil
}
