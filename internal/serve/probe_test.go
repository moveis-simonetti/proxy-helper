package serve

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

// validCredential is what fakeProxy accepts when it checks at all:
// "ana:certa" in the Basic form.
const validCredential = "Basic YW5hOmNlcnRh"

// fakeProxy answers CONNECT the way a corporate proxy does. requireAuth
// false is the case behind the bug report: it accepts anything, so a wrong
// password used to be reported as working.
func fakeProxy(t *testing.T, requireAuth bool) proxy.Config {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				authenticated := false
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.Contains(line, validCredential) {
						authenticated = true
					}
					if line == "\r\n" {
						break
					}
				}
				if requireAuth && !authenticated {
					_, _ = conn.Write([]byte("HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"p\"\r\nContent-Length: 0\r\n\r\n"))
					return
				}
				_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
			}()
		}
	}()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("splitting %s: %v", ln.Addr(), err)
	}
	return proxy.Config{Host: host, Port: port}
}

// The reported bug: a proxy that never checks credentials made the screen
// say "Funcionou" over a password the person had typed wrong.
func TestProbeDoesNotClaimSuccessWhenTheProxyNeverChecks(t *testing.T) {
	cfg := fakeProxy(t, false)

	got, _ := Probe(context.Background(), cfg, "ana", "senha-errada")

	if got == ProbeOK {
		t.Fatal("a proxy that never asked for credentials reported them as accepted")
	}
	if got != ProbeNoAuthRequired {
		t.Errorf("result = %v, want ProbeNoAuthRequired", got)
	}
}

// With no username there is nothing to verify.
func TestProbeReportsPlainSuccessWhenNoCredentialsWereGiven(t *testing.T) {
	cfg := fakeProxy(t, false)

	if got, _ := Probe(context.Background(), cfg, "", ""); got != ProbeOK {
		t.Errorf("result = %v, want ProbeOK", got)
	}
}

// A proxy that does check must refuse a wrong password, and that refusal
// must reach the screen as a refusal rather than as a vague failure.
func TestProbeReportsRefusalWhenTheProxyChecks(t *testing.T) {
	cfg := fakeProxy(t, true)

	if got, _ := Probe(context.Background(), cfg, "ana", "errada"); got != ProbeBadCredentials {
		t.Errorf("result = %v, want ProbeBadCredentials", got)
	}
}

// The credentials that do work must come back as plain success — the
// second, anonymous attempt is refused, which is what proves the proxy
// really checked.
func TestProbeAcceptsCredentialsTheProxyApproves(t *testing.T) {
	cfg := fakeProxy(t, true)

	if got, _ := Probe(context.Background(), cfg, "ana", "certa"); got != ProbeOK {
		t.Errorf("result = %v, want ProbeOK", got)
	}
}

// Nothing listening is not the same problem as a wrong password, and must
// not be reported as one.
func TestProbeReportsUnreachableWhenNothingAnswers(t *testing.T) {
	cfg := proxy.Config{Host: "127.0.0.1", Port: "1"}

	if got, _ := Probe(context.Background(), cfg, "ana", "x"); got != ProbeUnreachable {
		t.Errorf("result = %v, want ProbeUnreachable", got)
	}
}
