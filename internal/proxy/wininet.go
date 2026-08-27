// wininet.go carries the pure part of the WinINET target: turning a Config
// into the exact strings the registry expects. It has no build tag and
// imports nothing Windows-specific on purpose — a target that only compiles
// on Windows would have zero automated coverage on the machines this
// project is developed on, and the value formats are precisely where a
// silent mistake would hide (a wrong separator writes cleanly and simply
// fails to route traffic).
package proxy

import (
	"fmt"
	"strconv"
	"strings"
)

// WinINETTargetName is the name this target answers to in --targets.
const WinINETTargetName = "wininet"

// winINETProxyServer renders the ProxyServer value: "host:port" applied to
// every protocol.
//
// WinINET also accepts a per-protocol form ("http=host:port;https=host:port").
// This returns the single-address form because the Config models one proxy
// for every scheme, and the per-protocol form would only restate that.
// Credentials never appear here: they live in the local daemon, which is the
// whole reason this product exists on Windows.
func winINETProxyServer(cfg Config) (string, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return "", fmt.Errorf("proxy host is empty")
	}

	// A scheme prefix is valid in a proxy URL but not in ProxyServer's
	// single-address form, where WinINET reads it as part of the hostname
	// and then fails to resolve it.
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+len("://"):]
	}
	host = strings.Trim(host, "/")
	if host == "" {
		return "", fmt.Errorf("proxy host is empty")
	}

	port := strings.TrimSpace(cfg.Port)
	if port == "" {
		return "", fmt.Errorf("proxy port is empty")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return "", fmt.Errorf("proxy port %q is not a valid port number", cfg.Port)
	}

	return host + ":" + port, nil
}

// winINETProxyOverride renders the ProxyOverride value from a no-proxy list.
//
// The format differs from the Unix no_proxy this project uses everywhere
// else, and the differences are silent when wrong:
//
//   - entries are separated by ";" rather than ","
//   - a leading-dot domain suffix (".example.com") is written as "*.example.com"
//   - the literal token "<local>" — not a hostname — is what exempts
//     dotless local names, and Windows will not infer it
//
// CIDR blocks are passed through as-is: WinINET does not understand them,
// but dropping them silently would be worse than leaving a value an
// administrator can see and fix.
func winINETProxyOverride(noProxy []string) string {
	seen := make(map[string]bool)
	var out []string

	add := func(v string) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}

	for _, raw := range noProxy {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, ".") {
			add("*" + entry)
			continue
		}
		add(entry)
	}

	// Always last, so the wildcard entries read first in the registry value.
	add("<local>")
	return strings.Join(out, ";")
}
