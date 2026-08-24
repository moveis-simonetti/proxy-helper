// elevate.go has no "gui" build tag and imports no GTK, on purpose: the
// pieces here (which command to run, how to read its exit code) are pure
// enough to be tested without a display and without a real pkexec/polkit
// prompt. Keep it that way.
package gui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"proxy-helper/internal/serve"
)

// cliBinaryName is the CLI binary reinvoked under pkexec — never the GUI
// itself. The elevated process must not load GTK: pkexec runs as root, and
// a GTK app trying to open a display as root is exactly the kind of thing
// that goes wrong in unpredictable ways.
const cliBinaryName = "proxy-helper"

// findCLIBinary resolves the CLI binary to reinvoke: first next to the
// currently running binary (the normal install layout drops the GUI and the
// CLI in the same directory), then on PATH. It never guesses a path that
// was not actually found — a caller that gets an error here is expected to
// mark the privileged targets unavailable with a Notice explaining why,
// rather than trying a hardcoded fallback.
func findCLIBinary() (string, error) {
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), cliBinaryName)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath(cliBinaryName); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("could not find the %q CLI binary next to the GUI or on PATH", cliBinaryName)
}

// resolveConfigHome resolves the directory os.UserConfigDir() gives this
// (unprivileged) GUI process, so applyPrivileged can thread it through to
// the elevated "proxy set" as XDG_CONFIG_HOME. See elevateCmd's doc comment
// for why that is necessary.
func resolveConfigHome() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving config dir: %w", err)
	}
	return dir, nil
}

// elevateCmd builds the single elevated invocation covering every selected
// target that needs root. One pkexec call authorises all of them at once:
// pkexec does not cache credentials, so a call per target would stack up a
// password dialog per target.
//
// pkexec runs the reinvoked CLI in a minimal environment with HOME=/root,
// so a plain "pkexec binary proxy set --profile X" resolves the profile
// file through os.UserConfigDir() as /root/.config/proxy-helper/config.json
// — profile X does not exist there, so every privileged apply fails right
// after the user types their password. Wrapping the call as
// "pkexec env XDG_CONFIG_HOME=<dir> binary ..." fixes that: ConfigFilePath
// resolves through os.UserConfigDir(), which honours XDG_CONFIG_HOME.
// xdgConfigHome must be resolved by the caller (via resolveConfigHome, which
// runs as the invoking user) — never guessed here.
//
// --profile is deliberate, not --host/--user/--pass: passing the resolved
// credentials as command-line arguments would put the proxy password in
// `ps` output for any user on the machine to read. Do not "fix" the config
// lookup problem above by switching to --host/--pass.
//
// --via-local is never passed here, and never should be: under pkexec this
// process runs as root, with no XDG_RUNTIME_DIR and no user systemd
// manager, so DaemonActive() always reports false even when the daemon is
// running as the invoking user — app.Apply then fails every privileged
// target with a misleading "the local proxy is not running" error. Worse,
// if that check ever did pass, the elevated process would rewrite the
// user's config.json/​.lock as root, breaking every later unprivileged
// write. When the caller wants the privileged targets pointed at the local
// daemon, it does not pass --via-local at all: it resolves the loopback (or
// Docker bridge) address itself and calls elevateViaLocalCmd/
// elevateViaLocalCmds below with an explicit --host, which sidesteps
// DaemonActive() and the profile/config lookup entirely.
func elevateCmd(binary, profile string, targets []string, xdgConfigHome string) []string {
	return []string{
		"pkexec", "env", "XDG_CONFIG_HOME=" + xdgConfigHome,
		binary, "proxy", "set",
		"--profile", profile,
		"--targets", strings.Join(targets, ","),
	}
}

// elevateViaLocalCmd builds one elevated "proxy set --host ..." invocation
// that points targets at host:port — the loopback or Docker-bridge address
// of the local daemon — instead of using --via-local (see elevateCmd's
// comment for why --via-local itself is never used here).
//
// Passing the resolved address as an explicit --host/--port flag, rather
// than through --profile, is safe here in a way it would not be for
// --user/--pass: proxy.TargetConfig strips scheme/host/port/no-proxy down
// to a credential-free URL for every --via-local target, so there is no
// password in this URL for `ps` to leak — see page_status.go's apply() for
// the full reasoning. --host and --profile are mutually exclusive in
// "proxy set", so this shape never touches profile/config-file lookups at
// all: the elevated CLI does not need XDG_CONFIG_HOME to find a profile, it
// only needs it (still passed, for consistency with elevateCmd) so the
// no-proxy merge inside app.Apply reads the same global list the caller
// already merged against — see applyPrivilegedViaLocal's doc comment for
// the caveat when that merge diverges.
func elevateViaLocalCmd(binary, host, port string, noProxy, targets []string, xdgConfigHome string) []string {
	args := []string{
		"pkexec", "env", "XDG_CONFIG_HOME=" + xdgConfigHome,
		binary, "proxy", "set",
		"--host", host,
	}
	if port != "" {
		args = append(args, "--port", port)
	}
	if len(noProxy) > 0 {
		args = append(args, "--no-proxy", strings.Join(noProxy, ","))
	}
	return append(args, "--targets", strings.Join(targets, ","))
}

// selectedTargetInfo is the subset of a statusPageRow's state that
// splitSessionAware needs. It exists separately from statusPageRow
// (page_status.go, GTK-only, "gui"-tagged) so the elevated/in-process split
// can be unit tested without a display.
type selectedTargetInfo struct {
	Name          string
	Root          bool
	SessionScoped bool
}

// splitSessionAware splits targets by RequiresRoot into the user-level ones
// a page applies in-process and the privileged ones that go through a
// single pkexec call (see elevateCmd) — except a session-scoped target
// (gnome, kde; see proxy.Target.SessionScoped) is always treated as
// user-level here regardless of Root. pkexec runs with no $DISPLAY and no
// session D-Bus, so it can never reach the desktop session a session-scoped
// target needs — routing one there would silently fail the write instead
// of applying it in-process the way it actually works.
func splitSessionAware(targets []selectedTargetInfo) (user, privileged []string) {
	for _, t := range targets {
		if t.Root && !t.SessionScoped {
			privileged = append(privileged, t.Name)
		} else {
			user = append(user, t.Name)
		}
	}
	return user, privileged
}

// splitDockerTargets separates the targets that read their proxy settings
// from inside a container (serve.IsDockerTarget) from the rest. This reuses
// exactly the rule internal/app.ApplyViaLocal uses for the same split,
// rather than inventing a second one.
func splitDockerTargets(targets []string) (docker, other []string) {
	for _, name := range targets {
		if serve.IsDockerTarget(name) {
			docker = append(docker, name)
		} else {
			other = append(other, name)
		}
	}
	return docker, other
}

// elevateViaLocalCmds builds the elevated call(s) needed to point every
// selected privileged target at the local daemon.
//
// One call, loopback for everyone, covers the common case: Docker bridge
// disabled, or none of the selected privileged targets read their settings
// from inside a container. Two calls are needed only when Docker bridge is
// enabled AND a Docker target (dockerd) is among the selected privileged
// targets: dockerd needs the bridge address, since a container cannot reach
// the host's 127.0.0.1, while the rest still need loopback — a single
// --host cannot serve both. The second call is skipped when dockerd was
// the only privileged target selected.
func elevateViaLocalCmds(binary string, port int, noProxy, privileged []string, dockerBridgeEnabled bool, bridgeAddr, xdgConfigHome string) [][]string {
	portStr := strconv.Itoa(port)
	dockerTargets, other := splitDockerTargets(privileged)

	if !dockerBridgeEnabled || len(dockerTargets) == 0 {
		return [][]string{elevateViaLocalCmd(binary, "127.0.0.1", portStr, noProxy, privileged, xdgConfigHome)}
	}

	cmds := [][]string{elevateViaLocalCmd(binary, bridgeAddr, portStr, noProxy, dockerTargets, xdgConfigHome)}
	if len(other) > 0 {
		cmds = append(cmds, elevateViaLocalCmd(binary, "127.0.0.1", portStr, noProxy, other, xdgConfigHome))
	}
	return cmds
}

// needsTwoElevatedCalls reports, from inputs available before any pkexec
// call runs, whether elevateViaLocalCmds would produce two commands — so a
// caller can warn about a second polkit dialog before starting, instead of
// the user being surprised by an unannounced second password prompt.
func needsTwoElevatedCalls(privileged []string, dockerBridgeEnabled bool) bool {
	if !dockerBridgeEnabled {
		return false
	}
	dockerTargets, other := splitDockerTargets(privileged)
	return len(dockerTargets) > 0 && len(other) > 0
}

// elevateUnsetCmd is elevateCmd's counterpart for removing settings: no
// profile to pass, since "proxy unset" only ever needs the target list.
func elevateUnsetCmd(binary string, targets []string) []string {
	return []string{
		"pkexec", binary, "proxy", "unset",
		"--targets", strings.Join(targets, ","),
	}
}

// classifyExit turns a pkexec exit code into what the caller should tell the
// user.
//
//   - 126 means the user cancelled the polkit dialog, or was not authorised
//     to run it. That is not a failure of proxy-helper: whatever user
//     targets were already applied stay applied, and the privileged ones are
//     simply left untouched.
//   - 127 means pkexec itself could not execute the target program (missing
//     binary, not executable, polkit misconfigured, ...). That is an error.
//   - anything else is the reinvoked CLI's own exit status: zero is success,
//     non-zero is the CLI reporting its own failure.
func classifyExit(code int) (cancelled bool, err error) {
	switch code {
	case 0:
		return false, nil
	case 126:
		return true, nil
	case 127:
		return false, errors.New("pkexec could not execute the proxy-helper CLI")
	default:
		return false, fmt.Errorf("proxy-helper exited with status %d", code)
	}
}

// runCmd executes args[0] with the rest as arguments, captures its combined
// output, and classifies the result via classifyExit. It is the one place
// that actually shells out; kept separate from elevateCmd so a test can
// swap in a fake command (e.g. []string{"sh", "-c", "exit 126"}) instead of
// a real pkexec.
func runCmd(args []string) (output string, cancelled bool, err error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, runErr := cmd.CombinedOutput()

	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			// The command itself could not be started at all (binary not
			// found, not executable, ...) — distinct from it running and
			// exiting non-zero.
			return string(out), false, fmt.Errorf("running %s: %w", args[0], runErr)
		}
	}

	cancelled, err = classifyExit(code)
	return string(out), cancelled, err
}
