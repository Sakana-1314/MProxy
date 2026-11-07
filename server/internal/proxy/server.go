package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cdnproxy/internal/store"
)

// Options configures the proxy server.
type Options struct {
	Addr               string
	DataDir            string
	Store              *store.Store
	Logger             *stdlog.Logger
	DOHURL             string
	DOHSubnets         []string
	StatsFlushInterval time.Duration
}

// Server is the forward proxy that terminates client TLS and re-encrypts
// requests to configured CDN upstreams.
type Server struct {
	addr     string
	store    *store.Store
	ca       *CA
	matcher  *Matcher
	resolver *upstreamResolver
	stats    *trafficStats
	logger   *stdlog.Logger

	auth credentialCache

	adminMu      sync.RWMutex
	adminHandler http.Handler

	activeMu sync.Mutex
	active   map[*tunnelConn]struct{}
	tunnelWG sync.WaitGroup
	closing  atomic.Bool

	httpServer *http.Server
	listener   net.Listener

	transportsMu sync.Mutex
	transports   map[string]*http.Transport
}

// New creates the proxy server and loads rules and the local CA.
func New(opts Options) (*Server, error) {
	logger := opts.Logger
	if logger == nil {
		logger = stdlog.Default()
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = "."
	}
	ca, err := LoadOrCreateCA(filepath.Join(dataDir, "ca"))
	if err != nil {
		return nil, err
	}
	rules, err := opts.Store.AllEnabledRules()
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}

	s := &Server{
		addr:       opts.Addr,
		store:      opts.Store,
		ca:         ca,
		matcher:    NewMatcher(),
		resolver:   newUpstreamResolver(resolverOptions{DOHURL: opts.DOHURL, Subnets: opts.DOHSubnets, Logger: logger, Store: opts.Store}),
		logger:     logger,
		active:     make(map[*tunnelConn]struct{}),
		transports: make(map[string]*http.Transport),
	}
	s.matcher.Reload(rules)
	go s.resolver.Warm(targetsFromRules(rules))
	s.stats = newTrafficStats(opts.Store, logger, opts.StatsFlushInterval)
	s.httpServer = &http.Server{
		Handler:           http.HandlerFunc(s.handle),
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          logger,
	}
	return s, nil
}

// Start begins listening on the configured address.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = listener
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Printf("[proxy] 代理服务停止: %v", err)
		}
	}()
	s.logger.Printf("[proxy] HTTP 代理监听于 %s", listener.Addr().String())
	return nil
}

// Addr returns the actual listen address (useful when :0 was requested).
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}

// Shutdown stops accepting connections and closes active tunnels.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closing.Store(true)
	var firstErr error
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			firstErr = err
		}
	}
	s.activeMu.Lock()
	active := make([]*tunnelConn, 0, len(s.active))
	for conn := range s.active {
		active = append(active, conn)
	}
	s.activeMu.Unlock()
	for _, conn := range active {
		_ = conn.Close()
	}

	done := make(chan struct{})
	go func() {
		s.tunnelWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		if firstErr == nil {
			firstErr = ctx.Err()
		}
	}
	s.resolver.Close()
	s.stats.Close()

	s.transportsMu.Lock()
	for _, transport := range s.transports {
		transport.CloseIdleConnections()
	}
	s.transportsMu.Unlock()
	return firstErr
}

// ReloadRules refreshes the matcher from the database.
func (s *Server) ReloadRules() error {
	rules, err := s.store.AllEnabledRules()
	if err != nil {
		return err
	}
	s.matcher.Reload(rules)
	go s.resolver.Warm(targetsFromRules(rules))
	return nil
}

// InvalidateAuthCache clears cached proxy credentials after user changes.
func (s *Server) InvalidateAuthCache() { s.auth.Clear() }

// SetAdminHandler mounts the admin API/SPA on the shared proxy port. The
// caller must set it before Start; later updates are safe but unusual.
func (s *Server) SetAdminHandler(handler http.Handler) {
	s.adminMu.Lock()
	s.adminHandler = handler
	s.adminMu.Unlock()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	if r.URL != nil && r.URL.IsAbs() {
		s.handleForward(w, r)
		return
	}
	s.adminMu.RLock()
	admin := s.adminHandler
	s.adminMu.RUnlock()
	if admin != nil {
		admin.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	targetHost, targetPort, err := connectTarget(r)
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, "invalid CONNECT target")
		return
	}

	auth := s.authenticate(r)
	if auth.Status != 0 {
		writeProxyError(w, auth.Status, auth.Message)
		return
	}

	rule, ok := s.matcher.Match("https", targetHost, targetPort)
	if !ok {
		writeProxyError(w, http.StatusForbidden, "no matching rule for "+targetHost)
		return
	}
	upstream := effectiveUpstreamRule(rule, store.SchemeHTTPS)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeProxyError(w, http.StatusInternalServerError, "connection hijacking unsupported")
		return
	}
	rawConn, readWriter, err := hijacker.Hijack()
	if err != nil {
		s.logger.Printf("[proxy] hijack 失败: %v", err)
		return
	}
	if _, err := io.WriteString(rawConn, "HTTP/1.1 200 Connection Established\r\nProxy-Agent: MProxy\r\n\r\n"); err != nil {
		_ = rawConn.Close()
		return
	}

	tunnel := &tunnelConn{
		Conn: &bufferedConn{Conn: rawConn, reader: readWriter.Reader},
	}
	tunnel.onClose = func(bytesUp, bytesDown int64) {
		s.activeMu.Lock()
		registered := tunnel.registered
		if registered {
			delete(s.active, tunnel)
			tunnel.registered = false
		}
		s.activeMu.Unlock()
		if !registered {
			return
		}
		s.stats.Record(userID(auth.User), bytesUp+bytesDown)
		s.tunnelWG.Done()
	}

	// Register before the handshake so shutdown can close this tunnel.
	s.activeMu.Lock()
	if s.closing.Load() {
		s.activeMu.Unlock()
		_ = tunnel.Close()
		return
	}
	tunnel.registered = true
	s.active[tunnel] = struct{}{}
	s.tunnelWG.Add(1)
	s.activeMu.Unlock()

	tlsConn := tls.Server(tunnel, s.ca.TLSConfig(targetHost))
	handshakeCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	err = tlsConn.HandshakeContext(handshakeCtx)
	cancel()
	if err != nil {
		s.logger.Printf("[proxy] TLS 握手失败 %s: %v", targetHost, err)
		_ = tunnel.Close()
		return
	}

	inner := &http.Server{
		Handler:           s.innerHandler(upstream),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          stdlog.New(io.Discard, "", 0),
	}
	_ = inner.Serve(&oneConnListener{conn: tlsConn})
}

func (s *Server) innerHandler(rule store.Rule) http.Handler {
	transport := s.transportFor(rule)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			http.Error(w, "CONNECT is not allowed inside a tunnel", http.StatusMethodNotAllowed)
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.URL.Scheme = rule.UpstreamScheme
				pr.Out.URL.Host = upstreamAddress(rule)
				pr.Out.URL.User = nil
				pr.Out.Host = upstreamHostHeader(rule)
				pr.Out.Header.Del("X-Forwarded-For")
				pr.Out.Header.Del("X-Forwarded-Host")
				pr.Out.Header.Del("X-Forwarded-Proto")
				pr.Out.Header.Del("Forwarded")
				pr.Out.Header.Del("Proxy-Authorization")
				pr.Out.Header.Del("Proxy-Connection")
				pr.SetXForwarded()
			},
			Transport: transport,
			ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
				s.logger.Printf("[proxy] 上游请求失败 %s: %v", rule.UpstreamHost, err)
				rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
				rw.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(rw, "upstream request failed\n")
			},
			// Flush immediately so SSE / token streams are not batched.
			FlushInterval: -1,
		}
		proxy.ServeHTTP(w, r)
	})
}

func connectTarget(r *http.Request) (string, int, error) {
	authority := strings.TrimSpace(r.RequestURI)
	if authority == "" {
		authority = r.Host
	}
	if authority == "" && r.URL != nil {
		authority = r.URL.Host
	}
	if authority == "" {
		return "", 0, errors.New("missing CONNECT target")
	}
	if strings.HasPrefix(authority, "http://") || strings.HasPrefix(authority, "https://") {
		parsed, err := url.Parse(authority)
		if err != nil {
			return "", 0, errors.New("invalid CONNECT target")
		}
		authority = parsed.Host
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	host, port, err := splitAuthority(authority, 443)
	if err != nil {
		return "", 0, err
	}
	host = NormalizeHost(host)
	if host == "" {
		return "", 0, errors.New("empty CONNECT host")
	}
	return host, port, nil
}

func splitAuthority(authority string, defaultPort int) (string, int, error) {
	authority = strings.TrimSpace(authority)
	if authority == "" {
		return "", 0, errors.New("empty authority")
	}
	if strings.HasPrefix(authority, "[") {
		end := strings.Index(authority, "]")
		if end < 0 {
			return "", 0, errors.New("invalid IPv6 target")
		}
		host := authority[1:end]
		rest := authority[end+1:]
		if rest == "" {
			return host, defaultPort, nil
		}
		if !strings.HasPrefix(rest, ":") {
			return "", 0, errors.New("invalid target port")
		}
		port, err := parsePort(rest[1:], defaultPort)
		return host, port, err
	}
	colons := strings.Count(authority, ":")
	if colons == 0 {
		return authority, defaultPort, nil
	}
	if colons > 1 {
		// Bare IPv6 literal without brackets.
		return authority, defaultPort, nil
	}
	host, portText, err := net.SplitHostPort(authority)
	if err != nil {
		return "", 0, errors.New("invalid target host:port")
	}
	port, err := parsePort(portText, defaultPort)
	return host, port, err
}

func parsePort(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, errors.New("invalid target port")
	}
	return port, nil
}

func writeProxyError(w http.ResponseWriter, status int, message string) {
	if status == http.StatusProxyAuthRequired {
		w.Header().Set("Proxy-Authenticate", `Basic realm="MProxy"`)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n")
}

func userID(user *store.ProxyUser) int64 {
	if user == nil {
		return 0
	}
	return user.ID
}

type tunnelConn struct {
	net.Conn

	up         atomic.Int64
	down       atomic.Int64
	onClose    func(int64, int64)
	closeOnce  sync.Once
	registered bool // guarded by Server.activeMu
}

func (c *tunnelConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.up.Add(int64(n))
	}
	return n, err
}

func (c *tunnelConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.down.Add(int64(n))
	}
	return n, err
}

func (c *tunnelConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.Conn.Close()
		if c.onClose != nil {
			c.onClose(c.up.Load(), c.down.Load())
		}
	})
	return nil
}

// bufferedConn preserves bytes already buffered by net/http before Hijack.
type bufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// oneConnListener feeds one hijacked connection to a per-tunnel http.Server.
type oneConnListener struct {
	mu   sync.Mutex
	conn net.Conn
	used bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used {
		return nil, io.EOF
	}
	l.used = true
	return l.conn, nil
}

func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return dummyAddr("cdn-proxy-tunnel") }

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }
