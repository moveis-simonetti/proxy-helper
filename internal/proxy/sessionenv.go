package proxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sessionEnvFileName is the drop-in this target owns inside
// ~/.config/environment.d.
const sessionEnvFileName = "50-proxy-helper.conf"

// sessionEnvVars are the variables this target manages, in a fixed order.
// Unset needs the same list to clear them, so keeping it in one place stops
// the two halves from drifting apart.
var sessionEnvVars = []string{
	"HTTP_PROXY", "http_proxy",
	"HTTPS_PROXY", "https_proxy",
	"NO_PROXY", "no_proxy",
	"NODE_USE_ENV_PROXY",
}

// sessionEnvPath is the full path of the managed drop-in.
func sessionEnvPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "environment.d", sessionEnvFileName), nil
}

// renderSessionEnv builds the drop-in's contents. systemd parses this file
// itself — it is not sourced by a shell — so the lines are bare KEY=value:
// no "export", and no quoting, which systemd would read as part of the
// value.
func renderSessionEnv(cfg Config) (string, error) {
	proxyURL, err := cfg.URL()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Managed by proxy-helper. Edits are overwritten.\n")
	b.WriteString("# Read by systemd --user at login, so GUI apps launched from the\n")
	b.WriteString("# desktop inherit the proxy; shells get it from the shell target.\n")
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		fmt.Fprintf(&b, "%s=%s\n", name, proxyURL)
	}
	if np := cfg.NoProxyString(); np != "" {
		fmt.Fprintf(&b, "NO_PROXY=%s\n", np)
		fmt.Fprintf(&b, "no_proxy=%s\n", np)
	}
	// Node's global fetch ignores the classic variables unless this is set,
	// exactly as in the shell target (see the longer note there). An
	// Electron app's Node side hits the same wall.
	b.WriteString("NODE_USE_ENV_PROXY=1\n")
	return b.String(), nil
}

type sessionEnvTarget struct{}

func NewSessionEnvTarget() Target { return &sessionEnvTarget{} }

func (t *sessionEnvTarget) Name() string        { return "session-env" }
func (t *sessionEnvTarget) RequiresRoot() bool  { return false }
func (t *sessionEnvTarget) SessionScoped() bool { return true }

// Available reports whether there is a systemd --user manager to talk to.
// On a headless server, in a container, or over a plain SSH session there
// is none, and the target is skipped rather than failing — the same way kde
// and lxd bow out when their tooling is absent.
func (t *sessionEnvTarget) Available() bool {
	if !commandExists("systemctl") {
		return false
	}
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

// liveEnv returns the running user manager's environment, parsed into a
// map. It is the authority for Status: the drop-in file describes what the
// *next* login will get, while this is what an app launched right now
// actually inherits.
func liveEnv() (map[string]string, error) {
	out, err := exec.Command("systemctl", "--user", "show-environment").Output()
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		env[name] = value
	}
	return env, nil
}

func (t *sessionEnvTarget) Set(ex *Executor, cfg Config) error {
	content, err := renderSessionEnv(cfg)
	if err != nil {
		return err
	}
	path, err := sessionEnvPath()
	if err != nil {
		return err
	}
	if err := ex.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}

	// The file above is only read at the next login. Pushing the same
	// values into the running user manager is what makes the change visible
	// to apps launched from now on, without asking the user to log out.
	assignments, err := sessionEnvAssignments(cfg)
	if err != nil {
		return err
	}
	if err := ex.Run("systemctl", append([]string{"--user", "set-environment"}, assignments...)...); err != nil {
		return err
	}
	// Part of the GNOME session launches apps through D-Bus activation,
	// which carries its own environment; systemctl's copy does not reach it.
	return ex.Run("dbus-update-activation-environment", append([]string{"--systemd"}, sessionEnvVars...)...)
}

// sessionEnvAssignments renders the managed variables as KEY=value pairs,
// suitable for "systemctl --user set-environment".
func sessionEnvAssignments(cfg Config) ([]string, error) {
	proxyURL, err := cfg.URL()
	if err != nil {
		return nil, err
	}
	np := cfg.NoProxyString()

	var out []string
	for _, name := range sessionEnvVars {
		switch name {
		case "NO_PROXY", "no_proxy":
			if np == "" {
				continue
			}
			out = append(out, name+"="+np)
		case "NODE_USE_ENV_PROXY":
			out = append(out, name+"=1")
		default:
			out = append(out, name+"="+proxyURL)
		}
	}
	return out, nil
}

func (t *sessionEnvTarget) Unset(ex *Executor) error {
	path, err := sessionEnvPath()
	if err != nil {
		return err
	}
	// RemoveFile already treats a missing file as success, so unsetting a
	// target that was never set is a no-op, like every other target.
	if err := ex.RemoveFile(path); err != nil {
		return err
	}
	return ex.Run("systemctl", append([]string{"--user", "unset-environment"}, sessionEnvVars...)...)
}

func (t *sessionEnvTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "no systemd --user session"
		return st, nil
	}

	env, err := liveEnv()
	if err != nil {
		return st, err
	}
	live := env["HTTPS_PROXY"]
	if live == "" {
		live = env["HTTP_PROXY"]
	}
	st.Enabled = live != ""
	st.Detail = live
	if stale := sessionEnvStalePort(live); stale != "" {
		st.Detail = stale
	}
	return st, nil
}

// sessionEnvStalePort compares the port the live session is handing to new
// processes against the daemon port the profile actually configures, and
// returns a warning when they disagree.
//
// It exists because this target has a sharp edge the others don't: a
// process's environment is frozen at launch and cannot be rewritten from
// outside, so a port change leaves already-running GUI apps talking to the
// old one. The gap widens because changing the port in the GUI does not
// re-apply the targets, so the live session can trail the profile until the
// user re-applies. Left silent, that surfaces as "1Password stopped working"
// with nothing on screen connecting the two.
//
// An empty string means there is nothing worth saying: no live value, no
// via-local setup, an unreadable profile, or the ports already agree.
func sessionEnvStalePort(live string) string {
	if live == "" {
		return ""
	}
	pf, err := LoadProfiles()
	if err != nil || !pf.ViaLocal {
		return ""
	}
	want := fmt.Sprintf("%d", pf.EffectiveLocalPort())
	// The live value is a full URL; comparing the port alone keeps this
	// honest whether the host is loopback or the Docker bridge.
	if _, port, found := strings.Cut(live, "://"); found {
		if _, p, ok := strings.Cut(port, ":"); ok {
			if strings.TrimSuffix(p, "/") == want {
				return ""
			}
		}
	}
	return fmt.Sprintf("%s — profile uses port %s; re-apply, then relaunch GUI apps", live, want)
}
