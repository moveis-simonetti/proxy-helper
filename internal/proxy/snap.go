package proxy

import (
	"os"
	"os/exec"
	"strings"
)

type snapTarget struct{}

func NewSnapTarget() Target { return &snapTarget{} }

func (t *snapTarget) Name() string       { return "snap" }
func (t *snapTarget) RequiresRoot() bool { return true }
func (t *snapTarget) Available() bool    { return commandExists("snap") }

func (t *snapTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}
	return ex.RunPrivileged("snap", "set", "system",
		"proxy.http="+proxyURL,
		"proxy.https="+proxyURL,
	)
}

func (t *snapTarget) Unset(ex *Executor) error {
	return ex.RunPrivileged("snap", "unset", "system", "proxy.http", "proxy.https")
}

// snap requires root even to *read* system config ("error: access denied
// (try with sudo)"), unlike every other target here. Without elevate, a
// denied read is reported as unknown rather than misreported as "not set".
func (t *snapTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "snap not installed"
		return st, nil
	}

	var cmd *exec.Cmd
	if elevate && !IsRoot() {
		cmd = exec.Command("sudo", "snap", "get", "system", "proxy.http")
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr // let sudo's password prompt reach the terminal
	} else {
		cmd = exec.Command("snap", "get", "system", "proxy.http")
	}

	out, err := cmd.Output()
	value := strings.TrimSpace(string(out))
	if err != nil {
		if !elevate {
			if exitErr, ok := err.(*exec.ExitError); ok && strings.Contains(string(exitErr.Stderr), "access denied") {
				st.NeedsElevation = true
				st.Detail = "requires sudo to check"
				return st, nil
			}
		}
		st.Detail = "not set"
		return st, nil
	}
	if value == "" {
		st.Detail = "not set"
		return st, nil
	}
	st.Enabled = true
	st.Detail = redactSecrets(value)
	return st, nil
}
