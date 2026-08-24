package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// hopHeaders are headers a proxy must never forward (RFC 7230 §6.1) plus the
// relay control headers (which must not leak to the origin) and Content-Length
// (managed by http.Client).
var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade",
	"X-Relay-Target", "X-Relay-Path", "X-Relay-Secret",
	"Content-Length",
}

func isHopHeader(key string) bool {
	k := http.CanonicalHeaderKey(key)
	for _, h := range hopHeaders {
		if http.CanonicalHeaderKey(h) == k {
			return true
		}
	}
	return false
}

// copyHeaders copies src -> dst, dropping hop-by-hop and relay-control headers
// and any Content-Length that would conflict with the client's body handling.
func copyHeaders(dst, src http.Header) {
	for k := range dst {
		if isHopHeader(k) {
			dst.Del(k)
		}
	}
	for k, vs := range src {
		if isHopHeader(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// Relay configures the remote relay endpoint (the catch-all HTTP relay that takes
// x-relay-target / x-relay-path headers and fetches the target server-side).
type Relay struct {
	Base   string // relay base URL, e.g. https://relay-....vercel.app
	Secret string // optional x-relay-secret
	Client *http.Client
}

// NewRelay builds a Relay client. base is the relay base URL (scheme://host).
func NewRelay(base, secret string) *Relay {
	return &Relay{
		Base:   strings.TrimRight(base, "/"),
		Secret: secret,
		Client: &http.Client{
			// Never auto-follow redirects: pass Location back to the client.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy:                  nil, // never route through env proxy
				DisableCompression:     true, // pass origin content-encoding through as-is
				MaxIdleConnsPerHost:    100,
				IdleConnTimeout:        90 * time.Second,
				ResponseHeaderTimeout:  60 * time.Second,
				TLSHandshakeTimeout:    20 * time.Second,
			},
		},
	}
}

// Forward relays a single client request targeted at scheme://host/path through
// the remote relay and writes the origin's response to w.
//
// If w also implements http.Flusher (the normal server case), the body is
// streamed/flushed; a rawResponseWriter buffers it entirely instead.
func (r *Relay) Forward(w http.ResponseWriter, clientReq *http.Request, scheme, host, targetPath string) {
	// Read the body up front so we can set an accurate Content-Length on the
	// request we send to the relay. Serverless relays are HTTP request/response
	// only, so buffering a body is acceptable.
	var body []byte
	var err error
	if clientReq.Body != nil {
		body, err = io.ReadAll(clientReq.Body)
		if cerr := clientReq.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			http.Error(w, "relay-bridge: read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	relayURL := r.Base + "/relay"
	out, err := http.NewRequestWithContext(clientReq.Context(), clientReq.Method, relayURL, bytes.NewReader(body))
	if err != nil {
		http.Error(w, "relay-bridge: build request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	out.ContentLength = int64(len(body))
	copyHeaders(out.Header, clientReq.Header)

	// The relay control headers that define the real destination.
	out.Header.Set("x-relay-target", scheme+"://"+host)
	if targetPath != "" {
		out.Header.Set("x-relay-path", targetPath)
	}
	if r.Secret != "" {
		out.Header.Set("x-relay-secret", r.Secret)
	}

	resp, err := r.Client.Do(out)
	if err != nil {
		http.Error(w, "relay-bridge: relay request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flushCopy(w, resp.Body)
}

// flushCopy copies src to dst, flushing after each chunk so streaming responses
// (SSE, chunked, long-poll) reach the client promptly.
func flushCopy(w http.ResponseWriter, src io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

// --------------------------- raw connection writer ---------------------------

// rawResponseWriter plays the role of http.ResponseWriter but buffers the body and
// emits a single HTTP/1.1 response (with Content-Length + Connection: close) on a
// raw net.Conn. Used for CONNECT-to-HTTP and SOCKS5 CONNECT to port 80, where the
// client speaks origin-form HTTP directly on the tunnel.
type rawResponseWriter struct {
	conn        net.Conn
	header      http.Header
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (w *rawResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *rawResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
}

func (w *rawResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(p)
}

// finish flushes the buffered response to the conn as one HTTP/1.1 message.
func (w *rawResponseWriter) finish() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.header.Del("Transfer-Encoding")
	w.header.Del("Content-Length")
	w.header.Set("Content-Length", fmt.Sprintf("%d", w.body.Len()))
	w.header.Set("Connection", "close")
	_, _ = fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", w.status, http.StatusText(w.status))
	for k, vs := range w.header {
		for _, v := range vs {
			_, _ = fmt.Fprintf(w.conn, "%s: %s\r\n", k, v)
		}
	}
	_, _ = fmt.Fprint(w.conn, "\r\n")
	_, _ = w.conn.Write(w.body.Bytes())
}

// serveHTTPOverConn reads a single origin-form HTTP request from a raw conn,
// forwards it through the relay, and writes the response back. Used by the
// CONNECT-to-HTTP path and SOCKS5 CONNECT to port 80. The client believes it is
// talking directly to the origin, so requests arrive in origin-form with a Host
// header. One request per connection (then close) — the robust, keep-alive-free
// choice for serverless relays.
func serveHTTPOverConn(conn net.Conn, host, scheme string, relay *Relay) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	req.URL.Scheme = scheme
	if req.URL.Host == "" {
		req.URL.Host = host
	}
	targetPath := req.URL.RequestURI()
	if targetPath == "" {
		targetPath = "/"
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	rw := &rawResponseWriter{conn: conn}
	relay.Forward(rw, req, scheme, req.URL.Host, targetPath)
	rw.finish()
	req.Body.Close()
}
