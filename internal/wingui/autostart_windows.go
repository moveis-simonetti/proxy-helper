//go:build windows

package wingui

import (
	"fmt"
	"os"

	"proxy-helper/internal/proxy"
)

// autostartValue is the name of this app's entry in the Run key. It is
// separate from the daemon's entry on purpose: the proxy has to keep
// working whether or not anyone opened a window, so the two have
// independent lifetimes.
const autostartValue = "MS Proxy (bandeja)"

// runKeyPath is the per-user list of programs Windows starts at logon.
//
// The Run key and not a Scheduled Task: creating a task needs a right that
// domain policy commonly withholds, and being told "Acesso negado" for
// wanting an app to open at logon is not something the person can fix.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// InstallAutostart registers this binary to start hidden at logon.
//
// --hidden matters: without it the window would pop up in everyone's face
// every morning, which is how a useful app becomes one people uninstall.
func InstallAutostart(ex *proxy.Executor) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return ex.SetRegistryString(runKeyPath, autostartValue, fmt.Sprintf(`"%s" --hidden`, self))
}

// RemoveAutostart unregisters it. An entry that is not there is not an error.
func RemoveAutostart(ex *proxy.Executor) error {
	if err := ex.DeleteRegistryValue(runKeyPath, autostartValue); err != nil {
		return err
	}
	// Left over from when this was a Scheduled Task. Harmless to attempt,
	// and without it an older install keeps opening a second window.
	_ = ex.Run("schtasks", "/Delete", "/TN", autostartValue, "/F")
	return nil
}

// AutostartEnabled reports whether the entry exists.
func AutostartEnabled() bool {
	_, ok := executor().GetRegistryString(runKeyPath, autostartValue)
	return ok
}
