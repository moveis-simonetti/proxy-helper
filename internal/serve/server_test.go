package serve

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"proxy-helper/internal/proxy"
)

func discardLogger() *slog.Logger { return NewLogger(io.Discard, false) }

// proxyClient returns a client whose every request goes through the given
// proxy server, the way a tool configured with http_proxy would behave.
func proxyClient(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
}

func TestServerForwardsToUpstream(t *testing.T) {
	var gotAuth, gotVia, gotProxyConn string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		gotVia = r.Header.Get("Via")
		gotProxyConn = r.Header.Get("Proxy-Connection")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "hello from upstream")
	}))
	defer upstream.Close()

	cfg := proxy.Config{Scheme: "http", Host: mustHost(t, upstream.URL), Port: mustPort(t, upstream.URL)}
	router, _ := NewStaticRouter(cfg, "alice", "s3cr3t")
	local := httptest.NewServer(NewServer(stateFrom(router), discardLogger()).Handler())
	defer local.Close()

	req, _ := http.NewRequest("GET", "http://example.com/thing", nil)
	req.Header.Set("Proxy-Connection", "keep-alive")
	req.Header.Set("Proxy-Authorization", "Basic should-be-stripped")

	resp, err := proxyClient(t, local.URL).Do(req)
	if err != nil {
		t.Fatalf("request through local proxy: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from upstream" {
		t.Errorf("body = %q", body)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("upstream got Proxy-Authorization %q, want a Basic credential injected by the daemon", gotAuth)
	}
	if gotAuth == "Basic should-be-stripped" {
		t.Error("client's own Proxy-Authorization must be stripped, not forwarded")
	}
	if gotProxyConn != "" {
		t.Errorf("hop-by-hop Proxy-Connection leaked upstream: %q", gotProxyConn)
	}
	if !strings.Contains(gotVia, "proxy-helper") {
		t.Errorf("Via = %q, want it to mention proxy-helper", gotVia)
	}
}

func TestServerRoutesDirectForNoProxy(t *testing.T) {
	var reached bool
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		// A direct request must NOT carry proxy credentials.
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("direct request must not carry Proxy-Authorization")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer origin.Close()

	host := mustHost(t, origin.URL)
	cfg := proxy.Config{Scheme: "http", Host: "unreachable.invalid", Port: "9", NoProxy: []string{host}}
	router, _ := NewStaticRouter(cfg, "alice", "s3cr3t")
	local := httptest.NewServer(NewServer(stateFrom(router), discardLogger()).Handler())
	defer local.Close()

	resp, err := proxyClient(t, local.URL).Get(origin.URL)
	if err != nil {
		t.Fatalf("direct request: %v", err)
	}
	defer resp.Body.Close()
	if !reached {
		t.Error("origin was never reached; request did not go direct")
	}
}

func TestServerReturns502WhenUpstreamIsDown(t *testing.T) {
	cfg := proxy.Config{Scheme: "http", Host: "127.0.0.1", Port: "9"} // discard port, refuses
	router, _ := NewStaticRouter(cfg, "", "")
	local := httptest.NewServer(NewServer(stateFrom(router), discardLogger()).Handler())
	defer local.Close()

	resp, err := proxyClient(t, local.URL).Get("http://example.com/")
	if err != nil {
		t.Fatalf("expected a 502 response, got transport error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "127.0.0.1:9") {
		t.Errorf("502 body should name the failing upstream, got %q", body)
	}
}

func TestConnectTunnelsThroughUpstream(t *testing.T) {
	// A fake upstream proxy: accepts CONNECT, checks auth, then echoes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	authCh := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		authCh <- req.Header.Get("Proxy-Authorization")
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		io.Copy(conn, br) // echo whatever the tunnel carries
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	cfg := proxy.Config{Scheme: "http", Host: host, Port: port}
	router, _ := NewStaticRouter(cfg, "alice", "s3cr3t")
	local := httptest.NewServer(NewServer(stateFrom(router), discardLogger()).Handler())
	defer local.Close()

	// Speak CONNECT to our own proxy by hand.
	c, err := net.Dial("tcp", strings.TrimPrefix(local.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}

	select {
	case auth := <-authCh:
		if !strings.HasPrefix(auth, "Basic ") {
			t.Errorf("upstream CONNECT got Proxy-Authorization %q, want an injected Basic credential", auth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the CONNECT")
	}

	// The tunnel must carry bytes both ways.
	io.WriteString(c, "ping")
	buf := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("reading through tunnel: %v", err)
	}
	if string(buf) != "ping" {
		t.Errorf("tunnel echoed %q, want ping", buf)
	}
}

func TestConnectTunnelsThroughSOCKS5Upstream(t *testing.T) {
	// A minimal SOCKS5 server: no-auth method negotiation, then a CONNECT
	// request answered with success, then echo whatever the tunnel carries.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	connectedCh := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Method negotiation: VER NMETHODS METHODS...
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		nmethods := int(hdr[1])
		methods := make([]byte, nmethods)
		if _, err := io.ReadFull(conn, methods); err != nil {
			return
		}
		// Reply: VER=5, METHOD=0 (no auth).
		if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
			return
		}

		// CONNECT request: VER CMD RSV ATYP ADDR PORT.
		reqHdr := make([]byte, 4)
		if _, err := io.ReadFull(conn, reqHdr); err != nil {
			return
		}
		switch reqHdr[3] {
		case 0x01: // IPv4
			addr := make([]byte, 4+2)
			if _, err := io.ReadFull(conn, addr); err != nil {
				return
			}
		case 0x03: // domain name
			lenBuf := make([]byte, 1)
			if _, err := io.ReadFull(conn, lenBuf); err != nil {
				return
			}
			addr := make([]byte, int(lenBuf[0])+2)
			if _, err := io.ReadFull(conn, addr); err != nil {
				return
			}
		case 0x04: // IPv6
			addr := make([]byte, 16+2)
			if _, err := io.ReadFull(conn, addr); err != nil {
				return
			}
		default:
			return
		}

		// Reply: VER=5 REP=0(success) RSV ATYP=1(IPv4) 0.0.0.0:0.
		reply := []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
		if _, err := conn.Write(reply); err != nil {
			return
		}

		connectedCh <- struct{}{}
		io.Copy(conn, conn) // echo whatever the tunnel carries
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	cfg := proxy.Config{Scheme: "socks5", Host: host, Port: port}
	router, _ := NewStaticRouter(cfg, "", "")
	local := httptest.NewServer(NewServer(stateFrom(router), discardLogger()).Handler())
	defer local.Close()

	// Speak CONNECT to our own proxy by hand.
	c, err := net.Dial("tcp", strings.TrimPrefix(local.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}

	select {
	case <-connectedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("SOCKS5 upstream never completed the handshake")
	}

	// The tunnel must carry bytes both ways through the SOCKS5 dialer.
	io.WriteString(c, "ping")
	buf := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatalf("reading through SOCKS5 tunnel: %v", err)
	}
	if string(buf) != "ping" {
		t.Errorf("SOCKS5 tunnel echoed %q, want ping", buf)
	}
}

func TestConnectDirectReachesOrigin(t *testing.T) {
	origin, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		conn, err := origin.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.Copy(conn, conn)
	}()

	host, port, _ := net.SplitHostPort(origin.Addr().String())
	cfg := proxy.Config{Scheme: "http", Host: "unreachable.invalid", Port: "9", NoProxy: []string{host}}
	router, _ := NewStaticRouter(cfg, "", "")
	local := httptest.NewServer(NewServer(stateFrom(router), discardLogger()).Handler())
	defer local.Close()

	c, err := net.Dial("tcp", strings.TrimPrefix(local.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT %s:%s HTTP/1.1\r\nHost: %s:%s\r\n\r\n", host, port, host, port)

	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct CONNECT status = %d, want 200", resp.StatusCode)
	}
}

func TestConnectReturns502WhenUpstreamRefuses(t *testing.T) {
	cfg := proxy.Config{Scheme: "http", Host: "127.0.0.1", Port: "9"}
	router, _ := NewStaticRouter(cfg, "", "")
	local := httptest.NewServer(NewServer(stateFrom(router), discardLogger()).Handler())
	defer local.Close()

	c, err := net.Dial("tcp", strings.TrimPrefix(local.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")

	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func mustHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

func mustPort(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}
