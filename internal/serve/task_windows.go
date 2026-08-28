//go:build windows

package serve

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"proxy-helper/internal/proxy"
)

// TaskName is what the daemon is called in the Run key and in messages.
//
// Not a Windows Service: a service is installed machine-wide and needs
// administrator rights, while this product runs entirely as the logged-in
// user — which is where its credentials and its loopback listener belong.
const TaskName = "MS Proxy"

// UnitName is what the daemon is called in messages to the user. The Unix
// build names a systemd unit; here it is the scheduled task.
const UnitName = TaskName

// runKeyPath is where Windows keeps the per-user programs to start at
// logon. It is a plain HKCU value, so writing it needs no permission beyond
// the account's own registry — unlike Task Scheduler, which a managed
// account is often denied outright.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// InstallUnit makes the daemon start at every logon and starts it now.
//
// Deliberately not Task Scheduler any more. Creating a task needs a right
// that domain policy commonly withholds, and the failure — "ERRO: Acesso
// negado" — is one the person in front of the machine cannot act on. The
// Run key and a plain process launch need nothing they do not already have.
func InstallUnit(ex *proxy.Executor, execPath string, port int, dockerBridge bool) error {
	// dockerBridge has no meaning here: there is no Docker bridge to listen
	// on, and the daemon stays strictly on loopback.
	command := fmt.Sprintf(`"%s" proxy serve --port %d`, execPath, port)
	if err := ex.SetRegistryString(runKeyPath, TaskName, command); err != nil {
		return fmt.Errorf("registering the proxy to start at logon: %w", err)
	}

	// Starting is conditional, registering is not. A daemon someone started
	// by hand answers on the port and makes this look done, while nothing
	// would bring it back after a reboot — the machine works until it is
	// restarted, which is the worst moment to discover it.
	if DaemonActive() {
		return nil
	}
	return ex.StartDetached(execPath, "proxy", "serve", "--port", strconv.Itoa(port))
}

// UninstallUnit stops the daemon and unregisters it. Anything already gone
// is not an error: uninstalling twice should succeed.
func UninstallUnit(ex *proxy.Executor) error {
	_ = ex.DeleteRegistryValue(runKeyPath, TaskName)
	// Leftover from when this was a Scheduled Task: an install that
	// succeeded under the old scheme would otherwise keep starting a second
	// daemon at every logon, fighting for the port with this one.
	_ = ex.Run("schtasks", "/End", "/TN", TaskName)
	_ = ex.Run("schtasks", "/Delete", "/TN", TaskName, "/F")
	_ = ex.Run("taskkill", "/IM", "proxy-helper.exe", "/F")
	return nil
}

// AutostartRegistered reports whether the daemon will come back after a
// restart.
//
// Separate from DaemonActive because the two can disagree in the direction
// that hurts: a daemon running right now, with nothing registered to start
// it again, is a machine that works until it is rebooted.
func AutostartRegistered(ex *proxy.Executor) bool {
	value, ok := ex.GetRegistryString(runKeyPath, TaskName)
	return ok && value != ""
}

// DaemonActive reports whether the daemon is answering.
//
// It dials the port rather than asking Task Scheduler whether the task ran:
// a task can be registered and "last run successfully" while the process it
// started has since died. What callers actually need to know is whether the
// proxy is reachable.
func DaemonActive() bool {
	port := proxy.DefaultLocalPort
	if pf, err := proxy.LoadProfiles(); err == nil && pf.LocalPort > 0 {
		port = pf.LocalPort
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ReloadDaemon asks a running daemon to re-read its configuration.
func ReloadDaemon(ex *proxy.Executor) error {
	if ex != nil && ex.DryRun {
		return nil
	}
	return TriggerReload()
}

// UnitPath reports where the daemon's autostart is described. There is no
// file to point at on Windows, so this names the registry value.
func UnitPath() (string, error) {
	return `HKCU\` + runKeyPath + `\` + TaskName, nil
}

// StartHint is the command a person can run to start the daemon by hand.
// The Unix build names systemctl here; on Windows the daemon is a plain
// program, and printing a systemctl command was telling people to run
// something their machine does not have.
func StartHint() string {
	return `proxy-helper.exe proxy serve`
}
