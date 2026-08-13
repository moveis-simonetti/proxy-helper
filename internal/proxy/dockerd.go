package proxy

import (
	"fmt"
	"os"
	"strings"
)

// dockerdTarget configures the Docker daemon's own outbound proxy (used
// when dockerd pulls images) via a systemd drop-in. Restarting docker is
// left to the operator since it can disrupt running containers.
type dockerdTarget struct{}

func NewDockerdTarget() Target { return &dockerdTarget{} }

func (t *dockerdTarget) Name() string       { return "dockerd" }
func (t *dockerdTarget) RequiresRoot() bool { return true }

const dockerdDropInPath = "/etc/systemd/system/docker.service.d/http-proxy.conf"

func (t *dockerdTarget) Available() bool {
	return commandExists("systemctl") && commandExists("docker")
}

func (t *dockerdTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("[Service]\n")
	fmt.Fprintf(&b, "Environment=\"HTTP_PROXY=%s\"\n", proxyURL)
	fmt.Fprintf(&b, "Environment=\"HTTPS_PROXY=%s\"\n", proxyURL)
	if np := cfg.NoProxyString(); np != "" {
		fmt.Fprintf(&b, "Environment=\"NO_PROXY=%s\"\n", np)
	}

	if err := ex.WritePrivilegedFile(dockerdDropInPath, []byte(b.String()), 0o644); err != nil {
		return err
	}
	if err := ex.RunPrivileged("systemctl", "daemon-reload"); err != nil {
		return err
	}
	fmt.Println("  note: run `sudo systemctl restart docker` to apply (not done automatically, it restarts running containers)")
	return nil
}

func (t *dockerdTarget) Unset(ex *Executor) error {
	if err := ex.RemovePrivilegedFile(dockerdDropInPath); err != nil {
		return err
	}
	if err := ex.RunPrivileged("systemctl", "daemon-reload"); err != nil {
		return err
	}
	fmt.Println("  note: run `sudo systemctl restart docker` to apply")
	return nil
}

func (t *dockerdTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	content, err := readFileMaybePrivileged(dockerdDropInPath, elevate)
	if os.IsNotExist(err) {
		st.Detail = "not set"
		return st, nil
	}
	if err != nil {
		if !elevate && os.IsPermission(err) {
			st.NeedsElevation = true
			st.Detail = "requires sudo to check"
			return st, nil
		}
		st.Detail = fmt.Sprintf("error: %v", err)
		return st, nil
	}
	st.Enabled = true
	st.Detail = redactSecrets(firstEnvValue(string(content), "HTTP_PROXY"))
	return st, nil
}

// firstEnvValue extracts the value from the first `Environment="KEY=value"`
// line for the given key, for a compact one-line status summary.
func firstEnvValue(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		prefix := fmt.Sprintf(`Environment="%s=`, key)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimPrefix(line, prefix)
		return strings.TrimSuffix(value, `"`)
	}
	return strings.TrimSpace(content)
}
