package main

import (
	"net"
	"net/http"
)

// httpProxyHandler is an HTTP forward proxy. It translates standard proxy
// requests (absolute-form, e.g. "GET http://host/path HTTP/1.1") into relay
// calls. CONNECT is handled separately: only HTTP (port 80) targets can be
// bridged because the remote relay is HTTP-only and cannot carry raw TCP.
type httpProxyHandler struct {
	relay *Relay
}

func (h *httpProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Health / readiness endpoint (used by local verifiers and monitors).
	// Only triggers for origin-form requests (NOT absolute-form proxy requests,
	// which also have a "/" path and must be proxied, not answered here).
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		!r.URL.IsAbs() &&
		(r.URL.Path == "/" || r.URL.Path == "/health" || r.URL.Path == "/healthz") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"relay-bridge"}`))
		return
	}

	if r.Method == http.MethodConnect {
		h.handleConnect(w, r)
		return
	}

	// Absolute-form: the normal shape a client sends to a forward proxy.
	if r.URL.IsAbs() {
		scheme := r.URL.Scheme
		if scheme != "http" && scheme != "https" {
			http.Error(w, "relay-bridge: unsupported scheme "+scheme, http.StatusBadRequest)
			return
		}
		host := r.URL.Host
		if host == "" {
			http.Error(w, "relay-bridge: missing host in target URL", http.StatusBadRequest)
			return
		}
		targetPath := r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			targetPath += "?" + r.URL.RawQuery
		}
		if targetPath == "" {
			targetPath = "/"
		}
		h.relay.Forward(w, r, scheme, host, targetPath)
		return
	}

	// Not a valid forward-proxy request.
	http.Error(w, "relay-bridge: send absolute-form requests (scheme://host/path) to the proxy", http.StatusBadRequest)
}

// handleConnect handles HTTP CONNECT. A raw TCP tunnel cannot be built through
// an HTTP-only serverless relay, so only plain HTTP (port 80) targets are
// bridged (via request forwarding); HTTPS (port 443) and other ports are
// rejected with a clear error.
func (h *httpProxyHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
		port = "80"
	}
	if port != "80" && port != "443" {
		http.Error(w, "relay-bridge: CONNECT tunneling to port "+port+" is not supported by an HTTP-only relay", http.StatusBadGateway)
		return
	}
	if port == "443" {
		http.Error(w, "relay-bridge: HTTPS CONNECT tunneling is not supported by an HTTP-only relay; use an http:// target or absolute-form https", http.StatusBadGateway)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "relay-bridge: hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		http.Error(w, "relay-bridge: hijack failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Tell the client the tunnel is established, then forward its HTTP request
	// through the relay (one request per connection).
	_, _ = buf.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
	if err := buf.Flush(); err != nil {
		conn.Close()
		return
	}
	serveHTTPOverConn(conn, host, "http", h.relay)
}
