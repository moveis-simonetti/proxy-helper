//go:build windows

package serve

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"proxy-helper/internal/proxy"
)

// TaskName is the Scheduled Task that runs the daemon at logon.
//
// Task Scheduler rather than a Windows Service on purpose: a service is
// installed machine-wide and needs administrator rights, while this product
// deliberately runs entirely as the logged-in user. A logon task needs no
// elevation and starts the daemon in the right user's session, which is
// where its credentials and its loopback listener belong.
const TaskName = "MS Proxy"

// UnitName is what the daemon is called in messages to the user. The Unix
// build names a systemd unit; here it is the scheduled task.
const UnitName = TaskName

// InstallUnit registers the logon task and starts the daemon now, so the
// first run does not have to wait for a logoff/logon cycle.
func InstallUnit(ex *proxy.Executor, execPath string, port int, dockerBridge bool) error {
	// dockerBridge has no meaning here: there is no Docker bridge to listen
	// on, and the daemon stays strictly on loopback.
	command := fmt.Sprintf(`"%s" proxy serve --port %d`, execPath, port)

	if err := ex.Run("schtasks", "/Create",
		"/TN", TaskName,
		"/TR", command,
		"/SC", "ONLOGON",
		// Highest privileges are explicitly NOT requested: this must run as
		// the plain user, or it would write another account's registry.
		"/RL", "LIMITED",
		"/F", // replace an existing task instead of failing
	); err != nil {
		return fmt.Errorf("creating the %q task: %w", TaskName, err)
	}

	if err := ex.Run("schtasks", "/Run", "/TN", TaskName); err != nil {
		return fmt.Errorf("starting the %q task: %w", TaskName, err)
	}
	return nil
}

// UninstallUnit stops the daemon and removes the task. A task that is not
// there is not an error: uninstalling twice should succeed.
func UninstallUnit(ex *proxy.Executor) error {
	_ = ex.Run("schtasks", "/End", "/TN", TaskName)
	_ = ex.Run("schtasks", "/Delete", "/TN", TaskName, "/F")
	return nil
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

// UnitPath reports where the task is described. Windows keeps Scheduled
// Tasks in its own store rather than in a file the user edits, so this
// returns the task name for display instead of a path.
func UnitPath() (string, error) {
	return `Task Scheduler\` + TaskName, nil
}
