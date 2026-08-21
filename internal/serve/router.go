// Package serve implements the local forward proxy that other targets point
// at, so credentials live in one place instead of being copied into every
// tool's config file.
package serve

import (
	"fmt"
	"net"
	"strings"

	"proxy-helper/internal/proxy"
)

// Kind is how a request should leave this machine.
type Kind int

const (
	KindDirect Kind = iota
	KindHTTP
	KindSOCKS5
)

func (k Kind) String() string {
	switch k {
	case KindDirect:
		return "direct"
	case KindHTTP:
		return "http"
	case KindSOCKS5:
		return "socks5"
	}
	return "unknown"
}

// Upstream describes where a single request goes next.
type Upstream struct {
	Kind Kind
	Addr string // host:port; empty for KindDirect
	User string
	Pass string
}

// Router decides, per destination host, how the request leaves this machine.
// The interface exists so a PAC-evaluating router can replace StaticRouter
// later without touching the server.
type Router interface {
	Route(host string) Upstream
}

// StaticRouter routes from a fixed Config: anything matching the no-proxy
// list goes direct, everything else goes to the configured upstream.
type StaticRouter struct {
	upstream Upstream
	exact    map[string]bool
	suffixes []string
	nets     []*net.IPNet
}

// NewStaticRouter builds a router from cfg. An empty cfg.Host means every
// request routes direct, which is what backs "proxy off".
func NewStaticRouter(cfg proxy.Config, user, pass string) (*StaticRouter, error) {
	r := &StaticRouter{exact: map[string]bool{}}

	if cfg.Host != "" {
		kind := KindHTTP
		if strings.HasPrefix(cfg.Scheme, "socks") {
			kind = KindSOCKS5
		}
		addr := cfg.Host
		if cfg.Port != "" {
			addr = net.JoinHostPort(cfg.Host, cfg.Port)
		}
		r.upstream = Upstream{Kind: kind, Addr: addr, User: user, Pass: pass}
	}

	for _, entry := range cfg.NoProxy {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(entry); err == nil {
			r.nets = append(r.nets, ipnet)
			continue
		}
		if strings.HasPrefix(entry, ".") {
			r.suffixes = append(r.suffixes, strings.ToLower(entry))
			continue
		}
		r.exact[strings.ToLower(entry)] = true
	}
	return r, nil
}

// Upstream returns the upstream this router was configured with, ignoring
// the no-proxy list. It is KindDirect when no profile is active. Callers
// that want a per-host decision must use Route.
func (r *StaticRouter) Upstream() Upstream { return r.upstream }

// Route reports how to reach host. host must not include a port.
func (r *StaticRouter) Route(host string) Upstream {
	if r.upstream.Kind == KindDirect {
		return Upstream{Kind: KindDirect}
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))

	if r.exact[h] {
		return Upstream{Kind: KindDirect}
	}
	for _, suffix := range r.suffixes {
		// ".corp.com" matches both "a.corp.com" and bare "corp.com".
		if strings.HasSuffix(h, suffix) || h == strings.TrimPrefix(suffix, ".") {
			return Upstream{Kind: KindDirect}
		}
	}
	if ip := net.ParseIP(h); ip != nil {
		for _, n := range r.nets {
			if n.Contains(ip) {
				return Upstream{Kind: KindDirect}
			}
		}
	}
	return r.upstream
}

// String renders an upstream for logs. It never includes the password.
func (u Upstream) String() string {
	if u.Kind == KindDirect {
		return "DIRECT"
	}
	return fmt.Sprintf("%s://%s", u.Kind, u.Addr)
}
