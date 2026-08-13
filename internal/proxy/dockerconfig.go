package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// dockerConfigTarget sets the proxies injected into containers started by
// `docker run`/`docker build`, via ~/.docker/config.json. Unrelated keys
// (auths, credsStore, ...) are preserved.
type dockerConfigTarget struct{}

func NewDockerConfigTarget() Target { return &dockerConfigTarget{} }

func (t *dockerConfigTarget) Name() string       { return "docker-config" }
func (t *dockerConfigTarget) RequiresRoot() bool { return false }
func (t *dockerConfigTarget) Available() bool    { return true }

func (t *dockerConfigTarget) path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".docker", "config.json"), nil
}

func (t *dockerConfigTarget) read(path string) (map[string]interface{}, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return map[string]interface{}{}, nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return doc, nil
}

func (t *dockerConfigTarget) Set(ex *Executor, cfg Config) error {
	proxyURL, err := cfg.URL()
	if err != nil {
		return err
	}

	path, err := t.path()
	if err != nil {
		return err
	}
	doc, err := t.read(path)
	if err != nil {
		return err
	}

	entry := map[string]string{
		"httpProxy":  proxyURL,
		"httpsProxy": proxyURL,
	}
	if np := cfg.NoProxyString(); np != "" {
		entry["noProxy"] = np
	}
	doc["proxies"] = map[string]interface{}{"default": entry}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return ex.WriteFile(path, append(out, '\n'), 0o644)
}

func (t *dockerConfigTarget) Unset(ex *Executor) error {
	path, err := t.path()
	if err != nil {
		return err
	}
	doc, err := t.read(path)
	if err != nil {
		return err
	}
	if _, ok := doc["proxies"]; !ok {
		return nil
	}
	delete(doc, "proxies")

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return ex.WriteFile(path, append(out, '\n'), 0o644)
}

func (t *dockerConfigTarget) Status(elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: true}
	path, err := t.path()
	if err != nil {
		return st, err
	}
	doc, err := t.read(path)
	if err != nil {
		return st, err
	}
	proxies, ok := doc["proxies"].(map[string]interface{})
	if !ok {
		st.Detail = "not set"
		return st, nil
	}
	def, ok := proxies["default"].(map[string]interface{})
	if !ok {
		st.Detail = "not set"
		return st, nil
	}
	st.Enabled = true
	st.Detail = redactSecrets(fmt.Sprintf("%v", def["httpProxy"]))
	return st, nil
}
