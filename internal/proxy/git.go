package proxy

import (
	"os/exec"
	"strings"
)

type gitTarget struct{}

func NewGitTarget() Target { return &gitTarget{} }

func (t *gitTarget) Name() string       { return "git" }
func (t *gitTarget) RequiresRoot() bool { return false }

func (t *gitTarget) Available() bool { return commandExists("git") }

func (t *gitTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}
	if err := ex.Run("git", "config", "--global", "http.proxy", proxyURL); err != nil {
		return err
	}
	return ex.Run("git", "config", "--global", "https.proxy", proxyURL)
}

func (t *gitTarget) Unset(ex *Executor) error {
	if err := unsetGitConfig(ex, "http.proxy"); err != nil {
		return err
	}
	return unsetGitConfig(ex, "https.proxy")
}

// unsetGitConfig removes a git config key, treating "key not found" as success.
func unsetGitConfig(ex *Executor, key string) error {
	if ex.DryRun {
		return ex.Run("git", "config", "--global", "--unset", key)
	}
	cmd := exec.Command("git", "config", "--global", "--unset", key)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 5 {
			return nil // key was not set
		}
		return err
	}
	return nil
}

func (t *gitTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "git not installed"
		return st, nil
	}

	out, err := exec.Command("git", "config", "--global", "--get", "http.proxy").Output()
	value := strings.TrimSpace(string(out))
	if err != nil || value == "" {
		st.Detail = "not set"
		return st, nil
	}
	st.Enabled = true
	st.Detail = redactSecrets(value)
	return st, nil
}
