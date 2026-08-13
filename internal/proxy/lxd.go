package proxy

import (
	"fmt"
	"os/exec"
	"strings"
)

// lxdTarget configures the outbound proxy used by the LXD daemon (image
// downloads, container network access) via `lxc config`.
type lxdTarget struct{}

func NewLxdTarget() Target { return &lxdTarget{} }

func (t *lxdTarget) Name() string       { return "lxd" }
func (t *lxdTarget) RequiresRoot() bool { return false }
func (t *lxdTarget) Available() bool    { return commandExists("lxc") }

func (t *lxdTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}
	if err := ex.Run("lxc", "config", "set", "core.proxy_http", proxyURL); err != nil {
		return err
	}
	if err := ex.Run("lxc", "config", "set", "core.proxy_https", proxyURL); err != nil {
		return err
	}
	if np := cfg.NoProxyString(); np != "" {
		if err := ex.Run("lxc", "config", "set", "core.proxy_ignore_hosts", np); err != nil {
			return err
		}
	}
	return nil
}

func (t *lxdTarget) Unset(ex *Executor) error {
	if err := ex.Run("lxc", "config", "unset", "core.proxy_http"); err != nil {
		return err
	}
	if err := ex.Run("lxc", "config", "unset", "core.proxy_https"); err != nil {
		return err
	}
	return ex.Run("lxc", "config", "unset", "core.proxy_ignore_hosts")
}

func (t *lxdTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "lxc not installed"
		return st, nil
	}

	out, err := exec.Command("lxc", "config", "get", "core.proxy_http").Output()
	if err != nil {
		st.Detail = fmt.Sprintf("error: %v", err)
		return st, nil
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		st.Detail = "not set"
		return st, nil
	}
	st.Enabled = true
	st.Detail = redactSecrets(value)
	return st, nil
}
