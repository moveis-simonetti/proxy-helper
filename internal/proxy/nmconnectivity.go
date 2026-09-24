package proxy

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
//
// A configured URL only answers on the network it belongs to, though: on
// any other network — home, a phone hotspot, another office — it goes
// unreachable and NM reports "limited" again, this time wrongly, because
// the override is now testing the wrong thing. Set installs a
// NetworkManager dispatcher script alongside the override (see
// writeDispatcherScript) so that every network change re-probes the
// configured URL for real and toggles the override to match, instead of
// leaving it stuck pointing at whichever network it was configured on.
type nmConnectivityTarget struct{}

func NewNMConnectivityTarget() Target { return &nmConnectivityTarget{} }

func (t *nmConnectivityTarget) Name() string       { return "nm-connectivity" }
func (t *nmConnectivityTarget) RequiresRoot() bool { return true }

func (t *nmConnectivityTarget) SessionScoped() bool { return false }

// nmConnectivityConfPath is a var, not a const, so tests can point it at a
// throwaway path instead of the real /etc/NetworkManager/conf.d — the same
// seam aptConfDir uses in apt.go.
var nmConnectivityConfPath = "/etc/NetworkManager/conf.d/95-proxy-helper-connectivity.conf"

// nmDispatcherScriptPath is where the reconciliation script lives.
// NetworkManager runs every executable file directly under
// dispatcher.d/ (no subdirectories) on network changes; the number
// prefix is only a convention for humans reading the directory, dispatcher
// scripts have no ordering dependency on each other here.
var nmDispatcherScriptPath = "/etc/NetworkManager/dispatcher.d/90-proxy-helper-connectivity"

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
		_, confErr := os.Stat(nmConnectivityConfPath)
		_, scriptErr := os.Stat(nmDispatcherScriptPath)
		if os.IsNotExist(confErr) && os.IsNotExist(scriptErr) {
			return nil
		}
		return t.Unset(ex)
	}

	if err := t.applyConfFile(ex, cfg, true); err != nil {
		return err
	}
	return t.writeDispatcherScript(ex)
}

func (t *nmConnectivityTarget) Unset(ex *Executor) error {
	if err := t.applyConfFile(ex, Config{}, false); err != nil {
		return err
	}
	return ex.RemovePrivilegedFile(nmDispatcherScriptPath)
}

// applyConfFile writes or removes just the [connectivity] override,
// without touching the dispatcher script. Set and Unset both go through it
// (with the script's own lifecycle bolted on), and so does the
// "sync-connectivity" CLI command the dispatcher script invokes on every
// network change: that path must only toggle this file to match live
// reachability, never tear down the very script that is calling it.
func (t *nmConnectivityTarget) applyConfFile(ex *Executor, cfg Config, present bool) error {
	if !present {
		if err := ex.RemovePrivilegedFile(nmConnectivityConfPath); err != nil {
			return err
		}
		return ex.RunPrivileged("systemctl", "reload", "NetworkManager")
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

// writeDispatcherScript installs the script NetworkManager runs (as root,
// with none of the invoking user's environment) on every "up" and
// "connectivity-change" event. It shells out to this same binary's
// "sync-connectivity" command, with the configuring user's HOME and
// XDG_CONFIG_HOME pinned into the invocation — root has no config.json of
// its own, and guessing "whichever user is logged in right now" would be
// wrong the moment more than one account exists on the machine.
func (t *nmConnectivityTarget) writeDispatcherScript(ex *Executor) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving own path: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}

	script := fmt.Sprintf(`#!/bin/sh
# Installed by proxy-helper (target nm-connectivity). Do not edit by hand:
# rewritten by every "proxy profile enable" that configures
# connectivity_check_url, and removed automatically the moment no active
# profile does.
case "$2" in
	up|connectivity-change)
		HOME=%q XDG_CONFIG_HOME=%q %q proxy sync-connectivity >/dev/null 2>&1 || true
		;;
esac
`, home, configHome, execPath)

	return ex.WritePrivilegedFile(nmDispatcherScriptPath, []byte(script), 0o755)
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

// SyncNMConnectivity toggles just the [connectivity] override to match
// reachable, without touching the dispatcher script that (indirectly, via
// the "sync-connectivity" CLI command) calls it — the script must keep
// running on the next network change regardless of what today's
// reachability turns out to be. Exported for that command; nothing else
// should call it.
func SyncNMConnectivity(ex *Executor, cfg Config, reachable bool) error {
	t := &nmConnectivityTarget{}
	return t.applyConfFile(ex, cfg, reachable)
}

// ProbeConnectivityCheck reports whether url answers, within timeout, the
// way NetworkManager's own "response" option would consider a success: the
// body starts with wantPrefix, or — when wantPrefix is empty, mirroring
// NM's own documented fallback — the response is a bare 200 with no body,
// or a 204.
func ProbeConnectivityCheck(url, wantPrefix string, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if wantPrefix == "" {
		if resp.StatusCode == http.StatusNoContent {
			return true
		}
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var probe [1]byte
		n, _ := resp.Body.Read(probe[:])
		return n == 0
	}

	if resp.StatusCode != http.StatusOK {
		return false
	}
	buf := make([]byte, len(wantPrefix))
	n, _ := io.ReadFull(resp.Body, buf)
	return n == len(wantPrefix) && string(buf) == wantPrefix
}
