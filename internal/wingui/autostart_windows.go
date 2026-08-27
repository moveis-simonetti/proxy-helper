//go:build windows

package wingui

import (
	"fmt"
	"os"

	"proxy-helper/internal/proxy"
)

// autostartTaskName is the scheduled task that brings the tray icon back at
// logon. It is separate from the daemon's task on purpose: the proxy has to
// keep working whether or not anyone opened a window, so the two have
// independent lifetimes.
const autostartTaskName = "MS Proxy (bandeja)"

// InstallAutostart registers this binary to start hidden at logon.
//
// --hidden matters: without it the window would pop up in everyone's face
// every morning, which is how a useful app becomes one people uninstall.
func InstallAutostart(ex *proxy.Executor) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	command := fmt.Sprintf(`"%s" --hidden`, self)

	return ex.Run("schtasks", "/Create",
		"/TN", autostartTaskName,
		"/TR", command,
		"/SC", "ONLOGON",
		"/RL", "LIMITED",
		"/F",
	)
}

// RemoveAutostart unregisters it. A task that is not there is not an error.
func RemoveAutostart(ex *proxy.Executor) error {
	_ = ex.Run("schtasks", "/Delete", "/TN", autostartTaskName, "/F")
	return nil
}

// AutostartEnabled reports whether the task exists.
func AutostartEnabled() bool {
	_, err := executor().RunOutput("schtasks", "/Query", "/TN", autostartTaskName)
	return err == nil
}
