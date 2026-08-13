package proxy

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Config describes the proxy settings a user wants applied across targets.
type Config struct {
	Scheme   string   `json:"scheme,omitempty"` // http, https, socks5
	Host     string   `json:"host"`
	Port     string   `json:"port,omitempty"`
	Username string   `json:"user,omitempty"`
	Password string   `json:"pass,omitempty"`
	NoProxy  []string `json:"no_proxy,omitempty"`
}

// URL renders the proxy as a scheme://[user[:pass]@]host[:port] string.
func (c Config) URL() (string, error) {
	if c.Host == "" {
		return "", fmt.Errorf("proxy host is required")
	}
	scheme := c.Scheme
	if scheme == "" {
		scheme = "http"
	}

	host := c.Host
	if c.Port != "" {
		host = net.JoinHostPort(c.Host, c.Port)
	}

	u := &url.URL{Scheme: scheme, Host: host}
	if c.Username != "" {
		if c.Password != "" {
			u.User = url.UserPassword(c.Username, c.Password)
		} else {
			u.User = url.User(c.Username)
		}
	}
	return u.String(), nil
}

// NoProxyString joins the no-proxy hosts as a comma-separated list.
func (c Config) NoProxyString() string {
	return strings.Join(c.NoProxy, ",")
}

// MergeNoProxy combines the global no-proxy list with a profile/config-
// specific one, removing duplicates while preserving order (global entries
// first).
func MergeNoProxy(global, specific []string) []string {
	seen := make(map[string]bool, len(global)+len(specific))
	merged := make([]string, 0, len(global)+len(specific))
	add := func(hosts []string) {
		for _, h := range hosts {
			if h == "" || seen[h] {
				continue
			}
			seen[h] = true
			merged = append(merged, h)
		}
	}
	add(global)
	add(specific)
	return merged
}
