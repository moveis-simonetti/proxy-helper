package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// shellTarget manages a marker-delimited proxy export block in the user's
// shell rc files (~/.bashrc, ~/.zshrc). Only files that already exist are
// touched.
type shellTarget struct{}

func NewShellTarget() Target { return &shellTarget{} }

func (t *shellTarget) Name() string       { return "shell" }
func (t *shellTarget) RequiresRoot() bool { return false }

func (t *shellTarget) rcFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var found []string
	for _, name := range []string{".bashrc", ".zshrc"} {
		path := filepath.Join(home, name)
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
		}
	}
	return found
}

func (t *shellTarget) Available() bool {
	return len(t.rcFiles()) > 0
}

func (t *shellTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}

	var b strings.Builder
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		fmt.Fprintf(&b, "export %s=%q\n", name, proxyURL)
	}
	if np := cfg.NoProxyString(); np != "" {
		fmt.Fprintf(&b, "export NO_PROXY=%q\n", np)
		fmt.Fprintf(&b, "export no_proxy=%q\n", np)
	}

	files := t.rcFiles()
	if len(files) == 0 {
		return fmt.Errorf("no shell rc files found (~/.bashrc, ~/.zshrc)")
	}
	for _, path := range files {
		content, err := upsertBlock(path, b.String())
		if err != nil {
			return err
		}
		if err := ex.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (t *shellTarget) Unset(ex *Executor) error {
	for _, path := range t.rcFiles() {
		content, found, err := removeBlock(path)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if err := ex.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (t *shellTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: t.Available()}
	files := t.rcFiles()
	if len(files) == 0 {
		st.Detail = "no shell rc files found"
		return st, nil
	}

	var enabledIn []string
	var detail string
	for _, path := range files {
		body, found, err := readBlock(path)
		if err != nil {
			return st, err
		}
		if found {
			enabledIn = append(enabledIn, filepath.Base(path))
			if detail == "" {
				for _, line := range strings.Split(body, "\n") {
					if strings.HasPrefix(line, "export HTTP_PROXY=") {
						detail = strings.TrimPrefix(line, "export HTTP_PROXY=")
					}
				}
			}
		}
	}

	st.Enabled = len(enabledIn) > 0
	if st.Enabled {
		st.Detail = fmt.Sprintf("%s in %s", redactSecrets(detail), strings.Join(enabledIn, ", "))
	} else {
		st.Detail = fmt.Sprintf("not set (checked %s)", strings.Join(baseNames(files), ", "))
	}
	return st, nil
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}
