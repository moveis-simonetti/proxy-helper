package serve

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
	// ProbeNoAuthRequired: the proxy let the request through without ever
	// asking who was making it. The address works, but nothing here proves
	// the username and password are right — they were never checked.
	ProbeNoAuthRequired
)

// probeTarget is the host the probe asks the proxy to reach. Nothing is ever
// sent through the tunnel; the request exists to make the proxy answer.
const probeTarget = "example.com:443"

// probeTimeout bounds the whole exchange. A proxy that has not answered a
// CONNECT in this long is not going to.
const probeTimeout = 12 * time.Second

// Probe tries the configured proxy with the given credentials and reports
// which of the outcomes happened.
//
// It speaks CONNECT directly rather than driving an http.Client, because
// CONNECT is the exact exchange a browser performs first and the only part
// that depends on the credentials. Going through a client would additionally
// require the TLS handshake with the destination to succeed, turning an
// unrelated failure — a broken certificate chain, a blocked destination —
// into "your password is wrong".
//
// This exists because the alternative — saving the credentials and letting
// the user find out later that browsing is broken — puts the failure far
// away from its cause, at a moment when the person cannot connect it to
// what they typed.
func Probe(ctx context.Context, cfg proxy.Config, user, pass string) (ProbeResult, error) {
	upstream, err := cfg.URL()
	if err != nil {
		return ProbeUnreachable, err
	}
	parsed, err := url.Parse(upstream)
	if err != nil {
		return ProbeUnreachable, fmt.Errorf("the proxy address is not valid: %w", err)
	}
	addr := parsed.Host

	result, err := connectThrough(ctx, addr, authHeader(user, pass))
	if result != ProbeOK || user == "" {
		return result, err
	}

	// Getting through proves the address works. It does not prove the
	// credentials are right: a proxy that never asks accepts anything,
	// including a password someone mistyped. Asking again with no
	// credentials is what tells the two apart — and reporting "funcionou"
	// for a wrong password is worse than reporting nothing, because the
	// person stops looking for the real problem.
	if anonymous, _ := connectThrough(ctx, addr, ""); anonymous == ProbeOK {
		return ProbeNoAuthRequired, nil
	}
	return ProbeOK, nil
}

// authHeader renders the Proxy-Authorization value, or "" when there is no
// username to send.
func authHeader(user, pass string) string {
	if user == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// connectThrough opens one CONNECT and reports how the proxy answered.
func connectThrough(ctx context.Context, addr, auth string) (ProbeResult, error) {
	dialer := &net.Dialer{Timeout: probeTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return classifyProbeError(err), err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(probeTimeout))
	}

	request := "CONNECT " + probeTarget + " HTTP/1.1\r\nHost: " + probeTarget + "\r\n"
	if auth != "" {
		request += "Proxy-Authorization: " + auth + "\r\n"
	}
	request += "\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		return ProbeUnreachable, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		return ProbeUnreachable, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusProxyAuthRequired:
		return ProbeBadCredentials, nil
	case resp.StatusCode == http.StatusOK:
		return ProbeOK, nil
	default:
		// The proxy answered but would not open the tunnel: a blocked
		// destination, a policy refusal. The address works; this attempt
		// does not, and it is not the credentials.
		return ProbeUnreachable, fmt.Errorf("the proxy answered %s", resp.Status)
	}
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
	return ProbeUnreachable
}
