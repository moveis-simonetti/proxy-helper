package wingui

import (
	"fmt"
	"os"
	"path/filepath"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// cliBinaryName is the binary that runs the daemon. It is not this one: the
// interface and the proxy are separate programs, and the scheduled task must
// start the proxy, not a window.
const cliBinaryName = "proxy-helper"

// findCLI resolves the CLI next to this binary, then on PATH.
//
// It never falls back to a guessed path. A task registered against a binary
// that is not there would be registered successfully and fail silently at
// every logon — the machine would look configured and have no proxy.
func findCLI() (string, error) {
	name := cliBinaryName
	if isWindows {
		name += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), name)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := lookPath(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("não encontramos o programa que mantém o proxy funcionando (%s)", name)
}

// EnsureDaemon makes sure the local proxy is installed and answering.
//
// This has to run before the system proxy is pointed at 127.0.0.1: that
// address is the daemon, and without it every request would go to a port
// nothing listens on. The failure mode is the worst kind — the app reports
// success and the person loses internet access entirely.
func EnsureDaemon() error {
	if serve.DaemonActive() {
		return nil
	}
	if DryRun() {
		// Nothing was written, so nothing will answer. Waiting for a daemon
		// that was never installed would fail every action in the mode that
		// exists precisely to let someone try the actions.
		return nil
	}

	execPath, err := findCLI()
	if err != nil {
		return err
	}

	pf, err := proxy.LoadProfiles()
	if err != nil {
		return err
	}

	ex := executor()
	if err := serve.InstallUnit(ex, execPath, pf.EffectiveLocalPort(), false); err != nil {
		return fmt.Errorf("não foi possível iniciar o serviço do proxy: %w", err)
	}
	if !waitForDaemon() {
		// The bare "did not answer" names the symptom the person just saw.
		// What they need is why, and the daemon writes that down before it
		// gives up.
		if detail := daemonStartupDetail(); detail != "" {
			return fmt.Errorf("o serviço do proxy não conseguiu iniciar: %s", detail)
		}
		if where := daemonLogLocation(); where != "" {
			return fmt.Errorf("o serviço do proxy foi instalado mas não respondeu, e não registrou o motivo (log em %s)", where)
		}
		return fmt.Errorf("o serviço do proxy foi instalado mas não respondeu")
	}
	return nil
}
