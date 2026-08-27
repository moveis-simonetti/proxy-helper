//go:build windows

package proxy

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// winINETPath is the per-user key Edge, Chrome, Office and .NET read. It
// lives under HKCU, which is why nothing in this build needs administrator
// rights.
const winINETPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// WinINETTarget applies proxy settings to the Windows system proxy.
type WinINETTarget struct{}

// NewWinINETTarget builds the WinINET target.
func NewWinINETTarget() *WinINETTarget { return &WinINETTarget{} }

func (t *WinINETTarget) Name() string { return WinINETTargetName }

// RequiresRoot is false: every value written here is under HKCU.
func (t *WinINETTarget) RequiresRoot() bool { return false }

// SessionScoped is true: HKCU is the invoking user's hive, so applying this
// as another user would configure the wrong person's browser.
func (t *WinINETTarget) SessionScoped() bool { return true }

// Available opens the key rather than assuming it exists. It is present on
// every Windows install, but a policy-locked profile can deny write access,
// and reporting that as unavailable is better than failing mid-apply.
func (t *WinINETTarget) Available() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, winINETPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false
	}
	_ = key.Close()
	return true
}

func (t *WinINETTarget) Set(ex *Executor, cfg Config) error {
	if pac := strings.TrimSpace(cfg.PACURL); pac != "" {
		// A PAC URL and a fixed address are mutually exclusive in WinINET:
		// leaving ProxyEnable on alongside AutoConfigURL makes the fixed
		// address win, silently ignoring the PAC file.
		if err := ex.SetRegistryString(winINETPath, "AutoConfigURL", pac); err != nil {
			return err
		}
		if err := ex.SetRegistryUint32(winINETPath, "ProxyEnable", 0); err != nil {
			return err
		}
		return ex.RefreshWinINET()
	}

	server, err := winINETProxyServer(cfg)
	if err != nil {
		return err
	}
	if err := ex.SetRegistryString(winINETPath, "ProxyServer", server); err != nil {
		return err
	}
	if err := ex.SetRegistryString(winINETPath, "ProxyOverride", winINETProxyOverride(cfg.NoProxy)); err != nil {
		return err
	}
	if err := ex.SetRegistryUint32(winINETPath, "ProxyEnable", 1); err != nil {
		return err
	}
	// A stale AutoConfigURL would override the address just written.
	if err := ex.DeleteRegistryValue(winINETPath, "AutoConfigURL"); err != nil {
		return err
	}
	return ex.RefreshWinINET()
}

func (t *WinINETTarget) Unset(ex *Executor) error {
	// ProxyEnable goes to 0 and the address values are deleted, so a later
	// Status cannot report a stale address as if it were in effect.
	if err := ex.SetRegistryUint32(winINETPath, "ProxyEnable", 0); err != nil {
		return err
	}
	for _, name := range []string{"ProxyServer", "ProxyOverride", "AutoConfigURL"} {
		if err := ex.DeleteRegistryValue(winINETPath, name); err != nil {
			return err
		}
	}
	return ex.RefreshWinINET()
}

func (t *WinINETTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: true}

	key, err := registry.OpenKey(registry.CURRENT_USER, winINETPath, registry.QUERY_VALUE)
	if err != nil {
		st.Available = false
		st.Detail = "could not read the Windows proxy settings"
		return st, fmt.Errorf("opening %s: %w", winINETPath, err)
	}
	defer key.Close()

	enabled, _, _ := key.GetIntegerValue("ProxyEnable")
	server, _, _ := key.GetStringValue("ProxyServer")
	pac, _, _ := key.GetStringValue("AutoConfigURL")

	switch {
	case pac != "":
		st.Enabled = true
		st.Detail = "automatic configuration: " + pac
	case enabled == 1 && server != "":
		st.Enabled = true
		st.Detail = server
	case server != "":
		// Worth distinguishing: the address survives an Unset done by hand
		// in the Windows settings UI, and reporting a bare "off" would hide
		// that flipping the switch brings back an address nobody reviewed.
		st.Detail = "off (address kept: " + server + ")"
	default:
		st.Detail = "off"
	}
	return st, nil
}
