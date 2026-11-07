package proxy

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
)

func (s *Server) handleForward(w http.ResponseWriter, r *http.Request) {
	if r.URL == nil || !r.URL.IsAbs() || r.URL.Host == "" {
		http.Error(w, "this is an HTTP proxy; use absolute-form request URIs or CONNECT", http.StatusBadRequest)
		return
	}
	scheme := strings.ToLower(r.URL.Scheme)
	if scheme != "http" && scheme != "https" {
		http.Error(w, "unsupported URL scheme", http.StatusBadRequest)
		return
	}
	targetHost := NormalizeHost(r.URL.Hostname())
	if targetHost == "" {
		http.Error(w, "missing target host", http.StatusBadRequest)
		return
	}
	targetPort := 80
	if scheme == "https" {
		targetPort = 443
	}
	if portText := r.URL.Port(); portText != "" {
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			http.Error(w, "invalid target port", http.StatusBadRequest)
			return
		}
		targetPort = port
	}
	auth := s.authenticate(r)
	if auth.Status != 0 {
		writeProxyError(w, auth.Status, auth.Message)
		return
	}

	rule, ok := s.matcher.Match(scheme, targetHost, targetPort)
	if !ok {
		writeProxyError(w, http.StatusForbidden, "no matching rule for "+targetHost)
		return
	}

	// Strip proxy credentials and userinfo before forwarding.
	r.URL.User = nil
	r.Header.Del("Proxy-Authorization")
	r.Header.Del("Proxy-Connection")

	requestBytes := int64(0)
	if r.Body != nil {
		r.Body = &countingReadCloser{ReadCloser: r.Body, n: &requestBytes}
	}
	counter := &countingResponseWriter{ResponseWriter: w}
	upstream := effectiveUpstreamRule(rule, scheme)
	transport := s.transportFor(upstream)

	reverseProxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = upstream.UpstreamScheme
			pr.Out.URL.Host = upstreamAddress(upstream)
			pr.Out.URL.User = nil
			pr.Out.Host = upstreamHostHeader(upstream)
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
			s.logger.Printf("[proxy] 上游请求失败 %s: %v", upstream.UpstreamHost, err)
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(rw, "upstream request failed\n")
		},
		// Flush immediately after every write: LLM/SSE clients need each
		// token as soon as the upstream produces it, not in 100ms batches.
		FlushInterval: -1,
	}
	reverseProxy.ServeHTTP(counter, r)
	s.stats.Record(userID(auth.User), requestBytes+counter.bytesWritten)
}

type countingReadCloser struct {
	io.ReadCloser
	n *int64
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 {
		*c.n += int64(n)
	}
	return n, err
}

type countingResponseWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int64
	wroteHeader  bool
}

func (w *countingResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *countingResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.bytesWritten += int64(n)
	}
	return n, err
}

func (w *countingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *countingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *countingResponseWriter) FlushError() error {
	if flusher, ok := w.ResponseWriter.(interface{ FlushError() error }); ok {
		return flusher.FlushError()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
		return nil
	}
	return http.ErrNotSupported
}

func (w *countingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}
