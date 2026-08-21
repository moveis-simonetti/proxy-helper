package serve

import (
	"testing"

	"proxy-helper/internal/proxy"
)

func TestStaticRouterRoute(t *testing.T) {
	cfg := proxy.Config{
		Scheme:  "http",
		Host:    "proxy.corp",
		Port:    "8080",
		NoProxy: []string{"localhost", "127.0.0.1", ".corp.com", "gitlab.interno", "192.168.0.0/16"},
	}
	r, err := NewStaticRouter(cfg, "alice", "s3cr3t")
	if err != nil {
		t.Fatalf("NewStaticRouter: %v", err)
	}

	tests := []struct {
		host string
		want Kind
	}{
		{"github.com", KindHTTP},
		{"localhost", KindDirect},
		{"127.0.0.1", KindDirect},
		{"a.corp.com", KindDirect},
		{"corp.com", KindDirect},
		{"notcorp.com", KindHTTP},
		{"gitlab.interno", KindDirect},
		{"gitlab.interno.evil.com", KindHTTP},
		{"192.168.4.7", KindDirect},
		{"10.0.0.1", KindHTTP},
	}
	for _, tt := range tests {
		if got := r.Route(tt.host).Kind; got != tt.want {
			t.Errorf("Route(%q).Kind = %v, want %v", tt.host, got, tt.want)
		}
	}

	up := r.Route("github.com")
	if up.Addr != "proxy.corp:8080" {
		t.Errorf("Addr = %q, want %q", up.Addr, "proxy.corp:8080")
	}
	if up.User != "alice" || up.Pass != "s3cr3t" {
		t.Errorf("creds = %q/%q, want alice/s3cr3t", up.User, up.Pass)
	}
}

func TestStaticRouterEmptyHostIsAllDirect(t *testing.T) {
	r, err := NewStaticRouter(proxy.Config{}, "", "")
	if err != nil {
		t.Fatalf("NewStaticRouter: %v", err)
	}
	if got := r.Route("github.com").Kind; got != KindDirect {
		t.Errorf("empty config should route DIRECT, got %v", got)
	}
}

func TestStaticRouterSOCKS5(t *testing.T) {
	cfg := proxy.Config{Scheme: "socks5", Host: "socks.corp", Port: "1080"}
	r, _ := NewStaticRouter(cfg, "", "")
	if got := r.Route("github.com").Kind; got != KindSOCKS5 {
		t.Errorf("Kind = %v, want KindSOCKS5", got)
	}
}
