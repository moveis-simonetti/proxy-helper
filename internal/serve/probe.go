package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"proxy-helper/internal/proxy"
)

// ProbeResult says what happened when the credentials were tried against the
// upstream proxy.
type ProbeResult int

const (
	// ProbeOK: the proxy answered and accepted the credentials.
	ProbeOK ProbeResult = iota
	// ProbeBadCredentials: the proxy answered and refused them. It does not
	// say whether the username or the password was wrong, and neither do we.
	ProbeBadCredentials
	// ProbeUnknownHost: the address does not resolve. Almost always a typo.
	ProbeUnknownHost
	// ProbeUnreachable: the address resolves but nothing answered — proxy
	// down, wrong port, or no route to it from here.
	ProbeUnreachable
)

// probeURL is what the probe asks for. It is only ever used to make the
// proxy respond: any absolute URL works, and nothing about the response body
// is inspected.
const probeURL = "http://example.com/"

// Probe tries the configured proxy with the given credentials and reports
// which of the four outcomes happened.
//
// This exists because the alternative — saving the credentials and letting
// the user find out later that browsing is broken — puts the failure far
// away from its cause, at a moment when the person cannot connect it to
// what they typed. Everyone is set up on the same day, so "find out later"
// means a support queue.
func Probe(ctx context.Context, cfg proxy.Config, user, pass string) (ProbeResult, error) {
	upstream, err := cfg.URL()
	if err != nil {
		return ProbeUnreachable, err
	}
	parsed, err := url.Parse(upstream)
	if err != nil {
		return ProbeUnreachable, fmt.Errorf("the proxy address is not valid: %w", err)
	}
	if user != "" {
		parsed.User = url.UserPassword(user, pass)
	}

	client := &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(parsed),
			// No redirects, no keep-alive: one request, one answer.
			DisableKeepAlives: true,
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, probeURL, nil)
	if err != nil {
		return ProbeUnreachable, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return classifyProbeError(err), err
	}
	defer resp.Body.Close()

	// 407 is the proxy itself refusing the credentials. Any other status is
	// the origin server answering, which means the proxy let us through —
	// a 404 from example.com still proves the credentials worked.
	if resp.StatusCode == http.StatusProxyAuthRequired {
		return ProbeBadCredentials, nil
	}
	return ProbeOK, nil
}

// classifyProbeError turns a transport error into one of the outcomes.
//
// The distinction that matters to a person is "you typed the address wrong"
// versus "the address is right but nothing answered": the first is theirs to
// fix, the second is not.
func classifyProbeError(err error) ProbeResult {
	if err == nil {
		return ProbeOK
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ProbeUnknownHost
	}

	// A proxy that refuses the CONNECT with 407 surfaces as a transport
	// error rather than a response, because the tunnel is never
	// established. The status text is the only thing distinguishing it.
	if e := err.Error(); strings.Contains(e, "407") || strings.Contains(e, "Proxy Authentication Required") {
		return ProbeBadCredentials
	}

	return ProbeUnreachable
}
