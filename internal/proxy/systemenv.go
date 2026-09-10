package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// systemEnvTarget writes the proxy variables into /etc/environment, so every
// PAM login session on the machine inherits them: a console tty login,
// `ssh root@host`, `su -`, `sudo -i`, and — on Debian/Ubuntu, where
// /etc/pam.d/sudo pulls in pam_env — a plain `sudo <cmd>` too.
//
// This is the system-wide counterpart to session-env (which is per-user and
// covers the graphical session). It is the one lever that reaches root: the
// shell and session-env targets never touch anything root reads, so before
// this, `sudo apt update` behind a proxy-only network simply failed.
//
// The cost is real and deliberate: /etc/environment is not per-user, so
// every account's logins get these variables, not only the one who ran
// proxy-helper. That is usually what a proxied machine wants, but it is a
// wider blast radius than the other env targets — hence a separate,
// explicitly-named target rather than folding it into session-env.
type systemEnvTarget struct{}

func NewSystemEnvTarget() Target { return &systemEnvTarget{} }

func (t *systemEnvTarget) Name() string        { return "system-env" }
func (t *systemEnvTarget) RequiresRoot() bool  { return true }
func (t *systemEnvTarget) SessionScoped() bool { return false }

// etcEnvironmentPath is a seam for tests, mirroring apt's aptConfDir: the
// real path always exists on a Linux host, so pointing it elsewhere is the
// only way to exercise both Available() branches deterministically.
var etcEnvironmentPath = "/etc/environment"

// Available reports whether the directory holding /etc/environment exists.
// On any PAM-based Linux system it does; the check mainly bows out on the
// odd container image with no /etc, the same way apt bows out with no
// /etc/apt.
func (t *systemEnvTarget) Available() bool {
	_, err := os.Stat(filepath.Dir(etcEnvironmentPath))
	return err == nil
}

// renderSystemEnvBlock builds the managed block's body. pam_env parses
// /etc/environment itself, so the lines are bare KEY=value: no "export" (not
// a shell), and no quotes (pam_env keeps them as part of the value).
func renderSystemEnvBlock(cfg Config) (string, error) {
	proxyURL, err := cfg.URL()
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		fmt.Fprintf(&b, "%s=%s\n", name, proxyURL)
	}
	if np := cfg.NoProxyString(); np != "" {
		fmt.Fprintf(&b, "NO_PROXY=%s\n", np)
		fmt.Fprintf(&b, "no_proxy=%s\n", np)
	}
	// Node's global fetch ignores the classic variables unless this is set,
	// exactly as in the shell and session-env targets (see the longer note
	// in shell.go).
	b.WriteString("NODE_USE_ENV_PROXY=1\n")
	return b.String(), nil
}

func (t *systemEnvTarget) Set(ex *Executor, cfg Config) error {
	body, err := renderSystemEnvBlock(cfg)
	if err != nil {
		return err
	}
	// upsertBlock only reads the file (world-readable at 0644) and returns
	// the merged bytes; the privileged write is the one step that needs
	// root, and it goes through the Executor so --dry-run still works.
	content, err := upsertBlock(etcEnvironmentPath, body)
	if err != nil {
		return err
	}
	// Preview the block, not the merged file: /etc/environment is shared
	// with whatever else the machine keeps there, and a dry-run has no
	// business printing it.
	return ex.WritePrivilegedFilePreview(etcEnvironmentPath, content, body, 0o644)
}

func (t *systemEnvTarget) Unset(ex *Executor) error {
	content, found, err := removeBlock(etcEnvironmentPath)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return ex.WritePrivilegedFilePreview(etcEnvironmentPath, content,
		"(removing the proxy-helper managed block)", 0o644)
}

func (t *systemEnvTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	if !st.Available {
		st.Detail = "no /etc/environment"
		return st, nil
	}

	body, found, err := readBlock(etcEnvironmentPath)
	if os.IsNotExist(err) || !found {
		st.Detail = "not set"
		return st, nil
	}
	if err != nil {
		st.Detail = fmt.Sprintf("error: %v", err)
		return st, nil
	}

	st.Enabled = true
	detail := ""
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(line, "HTTP_PROXY="); ok {
			detail = v
			break
		}
	}
	st.Detail = redactSecrets(detail)
	return st, nil
}
