package proxy

import (
	"fmt"
	"os"
	"strings"
)

// nmConnectivityTarget points NetworkManager's own connectivity checker at a
// URL this specific network answers directly, without a proxy.
//
// NetworkManager's checker has no proxy support of its own: neither its
// per-connection PAC settings (proxy.method/proxy.pac-script, meant for
// distributing proxy config to other D-Bus consumers like pacrunner, not
// consumed by NM's own check) nor NetworkManager.conf offer one — confirmed
// by testing both. So on a network that requires a proxy for everything
// else, NM always reports "limited" connectivity in the GNOME/KDE network
// indicator even while this daemon is routing every real request through
// the proxy correctly. The only way to make NM agree with reality is to
// point its check at something reachable without a proxy, which is
// necessarily specific to one network's own quirks (an internal captive
// page, the proxy host's own web server on a different port, ...) — there
// is no way to discover it automatically, so it is opt-in per profile via
// Config.ConnectivityCheckURL, and left alone (this target no-ops) when
// that field is empty.
type nmConnectivityTarget struct{}

func NewNMConnectivityTarget() Target { return &nmConnectivityTarget{} }

func (t *nmConnectivityTarget) Name() string       { return "nm-connectivity" }
func (t *nmConnectivityTarget) RequiresRoot() bool { return true }

func (t *nmConnectivityTarget) SessionScoped() bool { return false }

// nmConnectivityConfPath is a var, not a const, so tests can point it at a
// throwaway path instead of the real /etc/NetworkManager/conf.d — the same
// seam aptConfDir uses in apt.go.
var nmConnectivityConfPath = "/etc/NetworkManager/conf.d/95-proxy-helper-connectivity.conf"

func (t *nmConnectivityTarget) Available() bool {
	return commandExists("nmcli")
}

func (t *nmConnectivityTarget) Set(ex *Executor, cfg Config) error {
	if cfg.ConnectivityCheckURL == "" {
		// The common case, by far: most profiles never configure this.
		// Checking existence ourselves (a plain, unprivileged stat — these
		// files are world-readable) keeps that case a true no-op instead of
		// an unconditional privileged remove-and-reload — asking for sudo
		// on every "proxy set" for a feature nobody turned on would be a
		// bad surprise. Only a profile switch away from one that DID
		// configure this needs the real cleanup in Unset.
		if _, err := os.Stat(nmConnectivityConfPath); os.IsNotExist(err) {
			return nil
		}
		return t.Unset(ex)
	}

	var b strings.Builder
	b.WriteString("[connectivity]\n")
	fmt.Fprintf(&b, "uri=%s\n", cfg.ConnectivityCheckURL)
	fmt.Fprintf(&b, "response=%s\n", cfg.ConnectivityCheckResponse)
	b.WriteString("interval=300\n")

	if err := ex.WritePrivilegedFile(nmConnectivityConfPath, []byte(b.String()), 0o644); err != nil {
		return err
	}
	// A config reload, not a restart: NetworkManager re-reads
	// NetworkManager.conf (including conf.d) on SIGHUP without dropping any
	// active connection.
	return ex.RunPrivileged("systemctl", "reload", "NetworkManager")
}

func (t *nmConnectivityTarget) Unset(ex *Executor) error {
	if err := ex.RemovePrivilegedFile(nmConnectivityConfPath); err != nil {
		return err
	}
	return ex.RunPrivileged("systemctl", "reload", "NetworkManager")
}

func (t *nmConnectivityTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "nmcli not found"
		return st, nil
	}

	content, err := ex.ReadFileMaybePrivileged(nmConnectivityConfPath, elevate)
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
	st.Detail = strings.TrimSpace(string(content))
	return st, nil
}
