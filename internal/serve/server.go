package serve

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	socks "golang.org/x/net/proxy"
)

// hopByHopHeaders must not be forwarded to the next hop. The client's own
// Proxy-Authorization is included on purpose: credentials on the outgoing
// hop are the daemon's business, never the client's.
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Proxy-Authorization",
	"Proxy-Authenticate",
	"Keep-Alive",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// Server is the local forward proxy. It listens on loopback only and does
// not authenticate its clients, so it must never be bound to a routable
// interface.
type Server struct {
	state  *State
	logger *slog.Logger

	mu         sync.Mutex
	transports map[string]*http.Transport
}

func NewServer(state *State, logger *slog.Logger) *Server {
	return &Server{state: state, logger: logger, transports: map[string]*http.Transport{}}
}

func (s *Server) Handler() http.Handler { return s }

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		s.handleConnect(w, req)
		return
	}
	s.handleForward(w, req)
}

func (s *Server) handleConnect(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	host := hostOnly(req.Host)
	port := portOnly(req.Host, "443")
	if host == "" {
		http.Error(w, "proxy-helper: malformed CONNECT target", http.StatusBadRequest)
		s.logger.Warn("bad_connect_target", slog.String("target", req.Host))
		return
	}
	up := s.state.Router().Route(host)

	upConn, err := s.dialUpstream(req.Context(), up, net.JoinHostPort(host, port))
	if err != nil {
		s.fail(w, up, host, req.Method, start, err)
		return
	}
	defer upConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy-helper: connection cannot be hijacked", http.StatusInternalServerError)
		return
	}
	clientConn, buf, err := hijacker.Hijack()
	if err != nil {
		s.logger.Error("hijack_failed", slog.String("error", err.Error()))
		return
	}
	defer clientConn.Close()

	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}

	// Anything the client already pipelined into the buffer must be
	// forwarded before we start copying raw.
	var in, out int64
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(upConn, buf)
		out = n
		if c, ok := upConn.(*net.TCPConn); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(clientConn, upConn)
		in = n
		if c, ok := clientConn.(*net.TCPConn); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done

	s.logger.Info("request", RequestEvent{
		Method: http.MethodConnect, Host: host, Port: port,
		Decision: up.Kind.String(), Upstream: up.String(),
		Status: http.StatusOK, BytesIn: in, BytesOut: out, Duration: time.Since(start),
	}.Attrs()...)
}

// dialUpstream opens the outbound leg of a CONNECT tunnel.
func (s *Server) dialUpstream(ctx context.Context, up Upstream, target string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	switch up.Kind {
	case KindDirect:
		return dialer.DialContext(ctx, "tcp", target)

	case KindSOCKS5:
		return socksDialContext(up)(ctx, "tcp", target)

	default:
		conn, err := dialer.DialContext(ctx, "tcp", up.Addr)
		if err != nil {
			return nil, err
		}
		connectReq := &http.Request{
			Method: http.MethodConnect,
			URL:    &url.URL{Opaque: target},
			Host:   target,
			Header: http.Header{},
		}
		if up.User != "" {
			connectReq.Header.Set("Proxy-Authorization", basicAuth(up.User, up.Pass))
		}
		if err := connectReq.Write(conn); err != nil {
			conn.Close()
			return nil, err
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), connectReq)
		if err != nil {
			conn.Close()
			return nil, err
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusProxyAuthRequired {
			conn.Close()
			s.logger.Warn("upstream_auth_failed", slog.String("upstream", up.String()), slog.String("host", target))
			return nil, fmt.Errorf("upstream proxy %s rejected the credentials (407)", up.Addr)
		}
		if resp.StatusCode != http.StatusOK {
			conn.Close()
			return nil, fmt.Errorf("upstream proxy %s answered CONNECT with %s", up.Addr, resp.Status)
		}
		return conn, nil
	}
}

func (s *Server) handleForward(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	host := hostOnly(req.Host)
	up := s.state.Router().Route(host)

	outReq := req.Clone(req.Context())
	outReq.RequestURI = ""
	stripHopByHop(outReq.Header)
	outReq.Header.Add("Via", "1.1 proxy-helper")
	if up.Kind == KindHTTP && up.User != "" {
		outReq.Header.Set("Proxy-Authorization", basicAuth(up.User, up.Pass))
	}

	resp, err := s.transportFor(up).RoundTrip(outReq)
	if err != nil {
		s.fail(w, up, host, req.Method, start, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusProxyAuthRequired {
		s.logger.Warn("upstream_auth_failed", slog.String("upstream", up.String()), slog.String("host", host))
		s.writeGatewayError(w, fmt.Sprintf(
			"upstream proxy %s rejected the credentials (407); check the active profile's username and password source",
			up.Addr))
		return
	}

	copyHeader(w.Header(), resp.Header)
	stripHopByHop(w.Header())
	w.WriteHeader(resp.StatusCode)
	written, _ := io.Copy(w, resp.Body)

	s.logger.Info("request", RequestEvent{
		Method: req.Method, Host: host, Port: portOnly(req.Host, "80"),
		Decision: up.Kind.String(), Upstream: up.String(),
		Status: resp.StatusCode, BytesOut: written, Duration: time.Since(start),
	}.Attrs()...)
}

func (s *Server) fail(w http.ResponseWriter, up Upstream, host, method string, start time.Time, err error) {
	target := up.Addr
	if up.Kind == KindDirect {
		target = host
	}
	s.logger.Error("request", RequestEvent{
		Method: method, Host: host, Decision: up.Kind.String(), Upstream: up.String(),
		Status: http.StatusBadGateway, Duration: time.Since(start), Err: err.Error(),
	}.Attrs()...)
	s.writeGatewayError(w, fmt.Sprintf("could not reach %s: %v", target, err))
}

// writeGatewayError answers with a plain-text 502. The body matters: it is
// what a developer sees in curl instead of a generic network error.
func (s *Server) writeGatewayError(w http.ResponseWriter, msg string) {
	http.Error(w, "proxy-helper: "+msg, http.StatusBadGateway)
}

func (s *Server) transportFor(up Upstream) *http.Transport {
	key := up.Kind.String() + "|" + up.Addr
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.transports[key]; ok {
		return t
	}

	t := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	switch up.Kind {
	case KindHTTP:
		t.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: up.Addr})
	case KindSOCKS5:
		t.DialContext = socksDialContext(up)
	}
	s.transports[key] = t
	return t
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func stripHopByHop(h http.Header) {
	// Connection names further headers that are themselves hop-by-hop.
	for _, name := range strings.Split(h.Get("Connection"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			h.Del(name)
		}
	}
	for _, name := range hopByHopHeaders {
		h.Del(name)
	}
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func portOnly(hostport, fallback string) string {
	if _, p, err := net.SplitHostPort(hostport); err == nil {
		return p
	}
	return fallback
}

// socksDialContext builds a dialer that reaches the target through a SOCKS5
// upstream. This is what lets apt, npm and docker — none of which speak
// SOCKS5 — use a SOCKS5 proxy.
func socksDialContext(up Upstream) func(ctx context.Context, network, addr string) (net.Conn, error) {
	var auth *socks.Auth
	if up.User != "" {
		auth = &socks.Auth{User: up.User, Password: up.Pass}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		d, err := socks.SOCKS5("tcp", up.Addr, auth, &net.Dialer{Timeout: 15 * time.Second})
		if err != nil {
			return nil, err
		}
		if cd, ok := d.(interface {
			DialContext(context.Context, string, string) (net.Conn, error)
		}); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return d.Dial(network, addr)
	}
}
