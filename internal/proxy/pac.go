package proxy

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// PACProxy is a single proxy server entry parsed out of a PAC (proxy
// auto-config) file.
type PACProxy struct {
	Scheme string // http, https, socks5
	Host   string
	Port   string
}

func (p PACProxy) String() string {
	return fmt.Sprintf("%s://%s:%s", p.Scheme, p.Host, p.Port)
}

// pacEntryRe matches the "PROXY host:port" style directives PAC files
// return from FindProxyForURL, e.g. "PROXY 10.0.0.5:8080", "SOCKS5
// 10.0.0.5:1080".
var pacEntryRe = regexp.MustCompile(`(?i)\b(PROXY|HTTPS|SOCKS5|SOCKS)\s+([a-zA-Z0-9.-]+):(\d+)`)

// FetchPAC downloads a PAC file from rawURL and extracts the distinct proxy
// servers it references. Credentials embedded in the URL
// (http://user:pass@host/proxy.pac) are sent as HTTP basic auth.
func FetchPAC(rawURL string) ([]PACProxy, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rawURL, err)
	}
	if u := req.URL.User; u != nil {
		password, _ := u.Password()
		req.SetBasicAuth(u.Username(), password)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: unexpected status %s", rawURL, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rawURL, err)
	}

	return ParsePAC(string(body))
}

// ParsePAC scans PAC (proxy auto-config) JavaScript for proxy directives and
// returns the distinct proxy servers found, in the order they first appear.
// PAC files are arbitrary JavaScript that can pick a proxy based on the
// requested URL; rather than evaluating them, this looks for the literal
// "PROXY/HTTPS/SOCKS[5] host:port" strings almost every PAC file returns
// from FindProxyForURL.
func ParsePAC(content string) ([]PACProxy, error) {
	var result []PACProxy
	seen := map[string]bool{}
	for _, m := range pacEntryRe.FindAllStringSubmatch(content, -1) {
		p := PACProxy{Scheme: pacScheme(m[1]), Host: m[2], Port: m[3]}
		key := p.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, p)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no PROXY/HTTPS/SOCKS entries found in PAC file")
	}
	return result, nil
}

func pacScheme(directive string) string {
	switch strings.ToUpper(directive) {
	case "HTTPS":
		return "https"
	case "SOCKS", "SOCKS5":
		return "socks5"
	default:
		return "http"
	}
}
