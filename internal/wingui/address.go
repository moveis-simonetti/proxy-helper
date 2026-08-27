package wingui

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"proxy-helper/internal/proxy"
)

// defaultProxyPort is assumed when the address carries no port.
//
// Asking a non-technical person to know that a proxy needs a port number is
// asking for a support ticket. 3128 is the overwhelmingly common default,
// and the probe verifies the guess immediately — so a wrong assumption
// surfaces as "não respondeu" while they are still on the screen, not days
// later.
const defaultProxyPort = "3128"

// ConfigFromAddress turns whatever the person typed into a Config.
//
// One field accepts both forms — "proxy.interno:3128" and the URL of a .pac
// file — because asking someone in management to classify their own proxy
// address as "manual" or "automatic" is a question they have no way to
// answer. They paste what they were given; this works out which it is.
func ConfigFromAddress(raw string) (proxy.Config, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return proxy.Config{}, fmt.Errorf("no address given")
	}

	if isPACAddress(addr) {
		return proxy.Config{PACURL: addr}, nil
	}

	// A scheme is valid in what someone pastes but is not part of the host.
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+len("://"):]
	}
	addr = strings.Trim(addr, "/")
	if addr == "" {
		return proxy.Config{}, fmt.Errorf("no address given")
	}

	host, port := addr, defaultProxyPort
	if h, p, err := net.SplitHostPort(addr); err == nil {
		host, port = h, p
	} else if strings.Contains(addr, ":") {
		// A colon that SplitHostPort rejected: "host:" or "host:abc".
		// Guessing a port here would hide a typo the probe would then
		// report as an outage.
		return proxy.Config{}, fmt.Errorf("%q is not a valid address", raw)
	}

	if host == "" {
		return proxy.Config{}, fmt.Errorf("%q has no host", raw)
	}
	if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
		return proxy.Config{}, fmt.Errorf("%q is not a valid port", port)
	}

	return proxy.Config{Scheme: "http", Host: host, Port: port}, nil
}

// isPACAddress reports whether the address names an auto-config file rather
// than a proxy to connect to.
//
// The test is the .pac extension on an http(s) URL, not the word "pac"
// anywhere: a proxy legitimately named "pac-proxy.interno" is a host, and
// treating it as a config file would break it in a way nobody could
// diagnose from the screen.
func isPACAddress(addr string) bool {
	lower := strings.ToLower(addr)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	u, err := url.Parse(addr)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(u.Path), ".pac")
}
