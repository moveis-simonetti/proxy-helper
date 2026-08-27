package proxy

import "testing"

// These run on every platform on purpose: the formats below are what makes
// the Windows target work or silently not route traffic, and the CI that
// gates this project runs on Linux.

func TestWinINETProxyServerRendersHostAndPort(t *testing.T) {
	got, err := winINETProxyServer(Config{Host: "proxy.interno", Port: "3128"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "proxy.interno:3128"; got != want {
		t.Errorf("ProxyServer = %q, want %q", got, want)
	}
}

func TestWinINETProxyServerStripsTheScheme(t *testing.T) {
	// A user pasting "http://proxy.interno" is the expected case, not an
	// edge one: every other target in this project accepts a URL.
	got, err := winINETProxyServer(Config{Host: "http://proxy.interno", Port: "3128"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "proxy.interno:3128"; got != want {
		t.Errorf("ProxyServer = %q, want %q — a scheme left in place makes WinINET treat it as part of the hostname", got, want)
	}
}

func TestWinINETProxyServerRejectsIncompleteConfigs(t *testing.T) {
	cases := map[string]Config{
		"no host":           {Host: "", Port: "3128"},
		"scheme only":       {Host: "http://", Port: "3128"},
		"no port":           {Host: "proxy.interno", Port: ""},
		"port not a number": {Host: "proxy.interno", Port: "3128a"},
		"port too large":    {Host: "proxy.interno", Port: "70000"},
		"port zero":         {Host: "proxy.interno", Port: "0"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := winINETProxyServer(cfg); err == nil {
				t.Errorf("expected an error, got %q — writing this into the registry breaks browsing with no message", got)
			}
		})
	}
}

func TestWinINETProxyOverrideUsesSemicolons(t *testing.T) {
	// The Unix no_proxy this project uses everywhere else is comma-separated.
	// A comma here writes cleanly and exempts nothing.
	got := winINETProxyOverride([]string{"localhost", "127.0.0.1"})
	if want := "localhost;127.0.0.1;<local>"; got != want {
		t.Errorf("ProxyOverride = %q, want %q", got, want)
	}
}

func TestWinINETProxyOverrideTurnsDomainSuffixesIntoWildcards(t *testing.T) {
	got := winINETProxyOverride([]string{".interno.br", "app.local"})
	if want := "*.interno.br;app.local;<local>"; got != want {
		t.Errorf("ProxyOverride = %q, want %q — WinINET does not read a leading dot as a suffix match", got, want)
	}
}

func TestWinINETProxyOverrideAlwaysAppendsTheLocalToken(t *testing.T) {
	// "<local>" is a literal token, not a hostname, and it is the only way
	// dotless names bypass the proxy. Windows never infers it.
	if got := winINETProxyOverride(nil); got != "<local>" {
		t.Errorf("ProxyOverride = %q, want %q for an empty list", got, "<local>")
	}
}

func TestWinINETProxyOverrideDoesNotRepeatEntries(t *testing.T) {
	got := winINETProxyOverride([]string{"localhost", "localhost", "<local>", ""})
	if want := "localhost;<local>"; got != want {
		t.Errorf("ProxyOverride = %q, want %q", got, want)
	}
}
