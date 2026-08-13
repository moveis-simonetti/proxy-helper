package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// npmTarget manages proxy/https-proxy/noproxy keys directly in ~/.npmrc,
// leaving every other line untouched.
type npmTarget struct{}

func NewNpmTarget() Target { return &npmTarget{} }

func (t *npmTarget) Name() string       { return "npm" }
func (t *npmTarget) RequiresRoot() bool { return false }
func (t *npmTarget) Available() bool    { return true }

func (t *npmTarget) path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".npmrc"), nil
}

var npmProxyKeys = []string{"proxy", "https-proxy", "noproxy"}

func (t *npmTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}

	values := map[string]string{
		"proxy":       proxyURL,
		"https-proxy": proxyURL,
	}
	if np := cfg.NoProxyString(); np != "" {
		values["noproxy"] = np
	}

	path, err := t.path()
	if err != nil {
		return err
	}
	lines, err := readNpmrcLines(path)
	if err != nil {
		return err
	}
	lines = filterNpmrcKeys(lines, npmProxyKeys)
	for _, key := range []string{"proxy", "https-proxy", "noproxy"} {
		if v, ok := values[key]; ok {
			lines = append(lines, fmt.Sprintf("%s=%s", key, v))
		}
	}
	return ex.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func (t *npmTarget) Unset(ex *Executor) error {
	path, err := t.path()
	if err != nil {
		return err
	}
	lines, err := readNpmrcLines(path)
	if err != nil {
		return err
	}
	filtered := filterNpmrcKeys(lines, npmProxyKeys)
	if len(filtered) == 0 {
		return ex.RemoveFile(path)
	}
	return ex.WriteFile(path, []byte(strings.Join(filtered, "\n")+"\n"), 0o644)
}

func (t *npmTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: true}
	path, err := t.path()
	if err != nil {
		return st, err
	}
	lines, err := readNpmrcLines(path)
	if err != nil {
		return st, err
	}
	for _, line := range lines {
		key, value, ok := parseNpmrcLine(line)
		if ok && key == "proxy" {
			st.Enabled = true
			st.Detail = redactSecrets(value)
		}
	}
	if !st.Enabled {
		st.Detail = "not set"
	}
	return st, nil
}

func readNpmrcLines(path string) ([]string, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(content), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func filterNpmrcKeys(lines []string, keys []string) []string {
	out := lines[:0:0]
	for _, line := range lines {
		key, _, ok := parseNpmrcLine(line)
		if ok && contains(keys, key) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func parseNpmrcLine(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
