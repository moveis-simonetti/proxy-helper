package proxy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dryRunPreview runs fn against a dry-run Executor and returns what the user
// would have seen.
func dryRunPreview(t *testing.T, fn func(*Executor) error) string {
	t.Helper()
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf, Stderr: &buf}
	if err := fn(ex); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	return buf.String()
}

// The bug: a dry-run preview echoed the whole merged file, so every unrelated
// secret already in it — an npm auth token, an editor's API key, a password
// in /etc/environment — was printed to the terminal (and into whatever log or
// transcript was capturing it) just for asking what a command would do.
//
// A preview's job is to show what CHANGES. Anything else in the file is not
// only a leak, it is noise.
func TestNpmDryRunDoesNotEchoTheWholeFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	secret := "npm_TESTONLYtoken000000000000000000000"
	npmrc := "//registry.npmjs.org/:_authToken=" + secret + "\nfoo=bar\n"
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte(npmrc), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	got := dryRunPreview(t, func(ex *Executor) error { return (&npmTarget{}).Set(ex, cfg) })

	if strings.Contains(got, secret) {
		t.Errorf("the npm auth token leaked into the preview:\n%s", got)
	}
	// It must still say what it is going to do.
	if !strings.Contains(got, "127.0.0.1:8888") {
		t.Errorf("preview does not show the proxy being set:\n%s", got)
	}
}

func TestVscodeDryRunDoesNotEchoTheWholeFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")

	userDir := filepath.Join(dir, "Code", "User")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := "sk-TESTONLYkey00000000000000000000000000"
	settings := `{"someExtension.apiKey": "` + secret + `", "editor.fontSize": 14}`
	if err := os.WriteFile(filepath.Join(userDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	got := dryRunPreview(t, func(ex *Executor) error { return (&vscodeTarget{}).Set(ex, cfg) })

	if strings.Contains(got, secret) {
		t.Errorf("an unrelated editor setting leaked into the preview:\n%s", got)
	}
	if !strings.Contains(got, "http.proxy") {
		t.Errorf("preview does not show the key being set:\n%s", got)
	}
}

func TestSystemEnvDryRunShowsOnlyItsBlock(t *testing.T) {
	withFakeEtcEnvironment(t, "PATH=/usr/bin\nAWS_SECRET_ACCESS_KEY=TESTONLYsecret000000\n")

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	got := dryRunPreview(t, func(ex *Executor) error { return (&systemEnvTarget{}).Set(ex, cfg) })

	if strings.Contains(got, "TESTONLYsecret000000") {
		t.Errorf("an unrelated environment variable leaked into the preview:\n%s", got)
	}
	if !strings.Contains(got, "HTTP_PROXY=http://127.0.0.1:8888") {
		t.Errorf("preview does not show the block being written:\n%s", got)
	}
}

func TestDockerConfigDryRunDoesNotEchoTheWholeFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".docker"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "TESTONLYregistryauth0000000000"
	cfgJSON := `{"auths":{"registry.example":{"auth":"` + secret + `"}}}`
	if err := os.WriteFile(filepath.Join(home, ".docker", "config.json"), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Scheme: "http", Host: "127.0.0.1", Port: "8888"}
	got := dryRunPreview(t, func(ex *Executor) error { return (&dockerConfigTarget{}).Set(ex, cfg) })

	if strings.Contains(got, secret) {
		t.Errorf("a registry credential leaked into the preview:\n%s", got)
	}
}

// Defense in depth: even where a whole file is legitimately echoed (a file
// this tool fully owns), a token that wandered into it must not print in the
// clear. These formats match nothing in the URL/JSON patterns.
func TestRedactSecretsCoversTokenAssignments(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"npmrc auth token", "//registry.npmjs.org/:_authToken=npm_TESTONLY0000000000000000000000"},
		{"npmrc basic auth", "//registry.example/:_auth=dGVzdG9ubHk6c2VjcmV0"},
		{"npmrc password", "//registry.example/:_password=TESTONLYsecret"},
		{"env style token", "GITHUB_TOKEN=ghp_TESTONLY000000000000000000000000"},
		{"env style secret", "AWS_SECRET_ACCESS_KEY=TESTONLYsecret000000"},
		{"env style api key", "OPENAI_API_KEY=sk-TESTONLY00000000000000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redactSecrets(c.in)
			if got == c.in {
				t.Errorf("redactSecrets left it untouched: %q", got)
			}
			if strings.Contains(got, "TESTONLY") {
				t.Errorf("the secret survived redaction: %q", got)
			}
			// The key must survive, or the preview stops being readable.
			key, _, _ := strings.Cut(c.in, "=")
			if !strings.Contains(got, key) {
				t.Errorf("redaction ate the key name too: %q", got)
			}
		})
	}
}

// Redaction must not mangle the ordinary lines a preview exists to show.
func TestRedactSecretsLeavesProxyLinesAlone(t *testing.T) {
	for _, in := range []string{
		"HTTP_PROXY=http://127.0.0.1:8888",
		"proxy=http://127.0.0.1:8888",
		"NO_PROXY=localhost,127.0.0.1",
		"NODE_USE_ENV_PROXY=1",
		"PATH=/usr/local/bin:/usr/bin",
	} {
		if got := redactSecrets(in); got != in {
			t.Errorf("redactSecrets(%q) = %q, want it unchanged", in, got)
		}
	}
}
