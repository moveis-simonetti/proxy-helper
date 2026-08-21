package proxy

import (
	"strings"
	"testing"
)

// upstream is the config a corporate profile would hold: a real host and a
// password that must never reach a tool's config file.
var upstream = Config{
	Scheme:   "http",
	Host:     "proxy.corp",
	Port:     "8080",
	Username: "alice",
	Password: "s3cr3t",
	NoProxy:  []string{"localhost", ".internal"},
}

func TestTargetConfigViaLocalHidesTheUpstream(t *testing.T) {
	got := TargetConfig(upstream, true, DefaultLocalPort)

	if got.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", got.Host)
	}
	if got.Port != "8888" {
		t.Errorf("Port = %q, want 8888", got.Port)
	}
	if got.Scheme != "http" {
		t.Errorf("Scheme = %q, want http", got.Scheme)
	}
	if got.Username != "" || got.Password != "" {
		t.Errorf("credential survived into the target config: user=%q pass=%q", got.Username, got.Password)
	}

	url, err := got.URL()
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	for _, secret := range []string{"s3cr3t", "alice", "proxy.corp"} {
		if strings.Contains(url, secret) {
			t.Errorf("target URL %q leaks %q", url, secret)
		}
	}
}

func TestTargetConfigViaLocalKeepsNoProxy(t *testing.T) {
	got := TargetConfig(upstream, true, DefaultLocalPort)
	if got.NoProxyString() != "localhost,.internal" {
		t.Errorf("NoProxy = %q, want the list to survive so local traffic skips the hop", got.NoProxyString())
	}
}

func TestTargetConfigUsesTheConfiguredPort(t *testing.T) {
	if got := TargetConfig(upstream, true, 9999); got.Port != "9999" {
		t.Errorf("Port = %q, want 9999", got.Port)
	}
	// A port that was never recorded falls back to the default rather than
	// producing "127.0.0.1:0".
	if got := TargetConfig(upstream, true, 0); got.Port != "8888" {
		t.Errorf("Port = %q, want the default 8888", got.Port)
	}
}

func TestTargetConfigWithoutViaLocalIsUnchanged(t *testing.T) {
	got := TargetConfig(upstream, false, 9999)
	if got.Host != "proxy.corp" || got.Password != "s3cr3t" {
		t.Errorf("legacy path must pass the config through untouched, got %+v", got)
	}
}

func TestEffectiveLocalPort(t *testing.T) {
	pf := &ProfileFile{}
	if got := pf.EffectiveLocalPort(); got != DefaultLocalPort {
		t.Errorf("EffectiveLocalPort() = %d, want %d", got, DefaultLocalPort)
	}
	pf.LocalPort = 9999
	if got := pf.EffectiveLocalPort(); got != 9999 {
		t.Errorf("EffectiveLocalPort() = %d, want 9999", got)
	}
}

func TestSelectsAllTargets(t *testing.T) {
	if !SelectsAllTargets([]string{"all"}) {
		t.Error(`"all" must count as a full selection`)
	}
	if SelectsAllTargets([]string{"git"}) {
		t.Error("a single target must not count as a full selection")
	}
	if SelectsAllTargets([]string{"git", "apt"}) {
		t.Error("a partial list must not count as a full selection")
	}
	if SelectsAllTargets([]string{"nonsense"}) {
		t.Error("an unresolvable list must not count as a full selection")
	}

	var every []string
	for _, target := range AllTargets() {
		every = append(every, target.Name())
	}
	if !SelectsAllTargets(every) {
		t.Error("naming every target must count as a full selection")
	}
}
