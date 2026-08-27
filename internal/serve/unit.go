package serve

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"proxy-helper/internal/proxy"
)

// UnitName is the systemd --user unit that runs the daemon.
const UnitName = "proxy-helper.service"

// UnitPath returns where the user unit lives. It does not check existence.
func UnitPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return filepath.Join(dir, "systemd", "user", UnitName), nil
}

// RenderUnit builds the unit file. ExecReload is what makes "proxy off" and
// profile switches instant: systemctl --user reload sends SIGHUP, and the
// daemon swaps its routing state without dropping connections.
func RenderUnit(execPath string, port int, dockerBridge bool) string {
	bridgeFlag := ""
	if dockerBridge {
		bridgeFlag = " --docker-bridge"
	}
	return fmt.Sprintf(`[Unit]
Description=proxy-helper local forward proxy
Documentation=https://github.com/gilbert/proxy-helper
After=network.target

[Service]
Type=simple
ExecStart=%s proxy serve --port %d%s
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=2
# The daemon binds loopback only and needs no privileges.
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
`, execPath, port, bridgeFlag)
}

// InstallUnit writes the unit and enables it. Every side effect goes through
// the Executor so --dry-run works.
func InstallUnit(ex *proxy.Executor, execPath string, port int, dockerBridge bool) error {
	path, err := UnitPath()
	if err != nil {
		return err
	}
	if err := ex.WriteFile(path, []byte(RenderUnit(execPath, port, dockerBridge)), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := ex.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %w", err)
	}
	if err := ex.Run("systemctl", "--user", "enable", "--now", UnitName); err != nil {
		return fmt.Errorf("enabling %s: %w", UnitName, err)
	}
	// "enable --now" starts a stopped service but leaves a running one alone,
	// so reinstalling with different flags would write a new ExecStart that
	// nothing is using. Restart explicitly to make the new unit take effect.
	if DaemonActive() {
		if err := ex.Run("systemctl", "--user", "restart", UnitName); err != nil {
			return fmt.Errorf("restarting %s: %w", UnitName, err)
		}
	}
	return nil
}

// UninstallUnit stops the daemon and removes its unit.
func UninstallUnit(ex *proxy.Executor) error {
	path, err := UnitPath()
	if err != nil {
		return err
	}
	// Ignore errors here: the unit may already be stopped or absent, and
	// that should not block removing the file.
	_ = ex.Run("systemctl", "--user", "disable", "--now", UnitName)
	if err := ex.RemoveFile(path); err != nil {
		return err
	}
	return ex.Run("systemctl", "--user", "daemon-reload")
}

// DaemonActive reports whether the unit is currently running.
func DaemonActive() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "is-active", "--quiet", UnitName).Run() == nil
}

// ReloadDaemon asks a running daemon to re-read its config. It is a no-op
// when the daemon is not running, so profile commands work the same whether
// or not the user opted into --via-local.
func ReloadDaemon(ex *proxy.Executor) error {
	if !DaemonActive() {
		return nil
	}
	return ex.Run("systemctl", "--user", "reload", UnitName)
}
