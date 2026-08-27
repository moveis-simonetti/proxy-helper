package proxy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Config describes the proxy settings a user wants applied across targets.
type Config struct {
	Scheme   string `json:"scheme,omitempty"` // http, https, socks5
	Host     string `json:"host"`
	Port     string `json:"port,omitempty"`
	Username string `json:"user,omitempty"`
	Password string `json:"pass,omitempty"`
	// PasswordFile and PasswordEnv keep the password out of config.json.
	// PasswordFile is the recommended form for the systemd user unit, which
	// does not inherit an interactive shell's environment.
	PasswordFile string `json:"password_file,omitempty"`
	PasswordEnv  string `json:"password_env,omitempty"`
	// PasswordProtected holds the password encrypted so that only the
	// current OS user account can read it back (Windows DPAPI). It is the
	// form the Windows build writes; on Linux the password keeps living in
	// a 0600 file, and a config carrying this field cannot be decrypted
	// there at all — by design, since the ciphertext is bound to a Windows
	// account.
	PasswordProtected string `json:"password_protected,omitempty"`
	// PACURL keeps the address of a proxy auto-config file when the user
	// configured one. On Linux "proxy import" resolves a PAC into Host/Port
	// and this stays empty; Windows hands the URL to WinINET as-is
	// (AutoConfigURL), which is better than resolving it once, because the
	// PAC can route different hosts to different proxies and can change
	// without the machine being reconfigured.
	PACURL  string   `json:"pac_url,omitempty"`
	NoProxy []string `json:"no_proxy,omitempty"`
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

// TargetConfig returns the config the targets should actually receive.
//
// Without viaLocal that is cfg itself, credentials included: that is the
// legacy behaviour, where every tool holds the real upstream.
//
// With viaLocal the targets get a credential-free loopback URL instead. The
// real upstream, username and password stay in config.json for the daemon to
// read, so they never reach ~/.gitconfig, ~/.npmrc, apt.conf.d and friends.
// The no-proxy list is still pushed down so local traffic skips the extra
// hop entirely.
func TargetConfig(cfg Config, viaLocal bool, port int) Config {
	return TargetConfigAt(cfg, viaLocal, port, "127.0.0.1")
}

// TargetConfigAt is TargetConfig with an explicit host, for the targets whose
// settings are read from inside a container and so cannot use loopback.
func TargetConfigAt(cfg Config, viaLocal bool, port int, host string) Config {
	if !viaLocal {
		return cfg
	}
	if port <= 0 {
		port = DefaultLocalPort
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return Config{
		Scheme:  "http",
		Host:    host,
		Port:    strconv.Itoa(port),
		NoProxy: cfg.NoProxy,
	}
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
