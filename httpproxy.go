package main

import (
	"crypto/tls"
	"net"
	"net/http"
)

// httpProxyHandler is an HTTP forward proxy. It translates standard proxy
// requests (absolute-form, e.g. "GET http://host/path HTTP/1.1") into relay
// calls. CONNECT is handled separately: with -mitm the proxy terminates the
// client-side TLS for HTTPS targets and forwards the decrypted request to the
// relay as an absolute-form https URL; otherwise only HTTP (port 80) can be
// bridged because the remote relay is HTTP-only and cannot carry raw TCP.
type httpProxyHandler struct {
	pool *RelayPool
	mitm *MITM // nil when -mitm is disabled
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
		h.pool.Forward(w, r, scheme, host, targetPath)
		return
	}

	// Not a valid forward-proxy request.
	http.Error(w, "relay-bridge: send absolute-form requests (scheme://host/path) to the proxy", http.StatusBadRequest)
}

// handleConnect handles HTTP CONNECT. With -mitm, HTTPS targets (port 443) are
// served by terminating the client's TLS and forwarding the decrypted request to
// the relay as an absolute-form https URL. Without -mitm (or for other ports),
// a raw TCP tunnel cannot be built through an HTTP-only serverless relay, so only
// plain HTTP (port 80) targets are bridged via request forwarding.
func (h *httpProxyHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
		port = "80"
	}
	switch port {
	case "443":
		if h.mitm != nil {
			h.serveMITM(w, host)
			return
		}
		http.Error(w, "relay-bridge: HTTPS CONNECT requires -mitm (enable TLS interception and trust the CA); otherwise use an http:// target", http.StatusBadGateway)
		return
	case "80":
		// HTTP over CONNECT: fall back to request forwarding via the relay.
	default:
		http.Error(w, "relay-bridge: CONNECT tunneling to port "+port+" is not supported by an HTTP-only relay", http.StatusBadGateway)
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
	// through the relay.
	_, _ = buf.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
	if err := buf.Flush(); err != nil {
		conn.Close()
		return
	}
	serveHTTPOverConn(conn, host, "http", h.pool)
}

// serveMITM answers CONNECT :443 with a 200, then terminates the client's TLS
// handshake using a leaf certificate for the requested host (signed by our CA),
// decrypts the HTTP requests, and forwards them to the relay as absolute-form
// https URLs. It does not build a raw TCP tunnel to the origin: the relay fetches
// the https URL server-side.
func (h *httpProxyHandler) serveMITM(w http.ResponseWriter, host string) {
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
	// Tell the client the tunnel is established (before the TLS handshake).
	_, _ = buf.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
	if err := buf.Flush(); err != nil {
		conn.Close()
		return
	}
	tlsConn := tls.Server(conn, h.mitm.TLSConfigForHost(host))
	if err := tlsConn.Handshake(); err != nil {
		// The client aborted the handshake (e.g. it does not trust our CA).
		conn.Close()
		return
	}
	// Decrypted traffic now flows over tlsConn; forward each HTTP request to the
	// relay as an https absolute-form target. Responses are re-encrypted by TLS.
	serveHTTPOverConn(tlsConn, host, "https", h.pool)
}
