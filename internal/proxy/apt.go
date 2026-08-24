package proxy

import (
	"fmt"
	"os"
	"strings"
)

type aptTarget struct{}

func NewAptTarget() Target { return &aptTarget{} }

func (t *aptTarget) Name() string       { return "apt" }
func (t *aptTarget) RequiresRoot() bool { return true }

func (t *aptTarget) SessionScoped() bool { return false }

const aptProxyPath = "/etc/apt/apt.conf.d/95proxies"

// aptConfDir is a seam for tests: Available() consults it instead of the
// literal "/etc/apt" so the unavailable path can be exercised even on
// Debian/Ubuntu hosts (and CI images) where /etc/apt always exists.
var aptConfDir = "/etc/apt"

func (t *aptTarget) Available() bool {
	_, err := os.Stat(aptConfDir)
	return err == nil
}

func (t *aptTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Acquire::http::Proxy %q;\n", proxyURL)
	fmt.Fprintf(&b, "Acquire::https::Proxy %q;\n", proxyURL)
	for _, host := range cfg.NoProxy {
		fmt.Fprintf(&b, "Acquire::http::Proxy::%s \"DIRECT\";\n", host)
		fmt.Fprintf(&b, "Acquire::https::Proxy::%s \"DIRECT\";\n", host)
	}

	return ex.WritePrivilegedFile(aptProxyPath, []byte(b.String()), 0o644)
}

func (t *aptTarget) Unset(ex *Executor) error {
	return ex.RemovePrivilegedFile(aptProxyPath)
}

func (t *aptTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "apt not installed"
		return st, nil
	}

	content, err := ex.ReadFileMaybePrivileged(aptProxyPath, elevate)
	if os.IsNotExist(err) {
		st.Detail = "not set"
		return st, nil
	}
	if err != nil {
		if !elevate && os.IsPermission(err) {
			st.NeedsElevation = true
			st.Detail = "requires sudo to check"
			return st, nil
		}
		st.Detail = fmt.Sprintf("error: %v", err)
		return st, nil
	}
	st.Enabled = true
	st.Detail = redactSecrets(strings.TrimSpace(string(content)))
	return st, nil
}
