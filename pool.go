package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
)

// RelayPool is a set of relays of a single type (vercel OR cloudflare — never
// both mixed). Depending on mode, incoming requests are either round-robined
// across the relays ("round_robin") or handled via persistent active cursor
// ("fallback"): requests stay on the currently active relay, and only shift
// forward to the next relay when the active one fails.
type RelayPool struct {
	relays []*Relay
	rr     atomic.Uint64
	cursor atomic.Uint64 // active relay index for fallback mode
	mode   string        // "round_robin" or "fallback"
}

// NewPool builds a pool from relay base URLs. Empty entries are skipped.
func NewPool(bases []string) *RelayPool {
	return NewPoolWithMode(bases, "", "round_robin")
}

// NewPoolWithSecret builds a pool from relay base URLs, attaching the secret to
// every relay. Empty entries are skipped.
func NewPoolWithSecret(bases []string, secret string) *RelayPool {
	return NewPoolWithMode(bases, secret, "round_robin")
}

// NewPoolWithMode builds a pool with an explicit mode ("round_robin" or "fallback").
func NewPoolWithMode(bases []string, secret, mode string) *RelayPool {
	p := &RelayPool{
		mode: normalizeMode(mode),
	}
	for _, b := range bases {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		p.relays = append(p.relays, NewRelay(b, secret))
	}
	return p
}

func normalizeMode(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	switch m {
	case "fallback", "failover", "priority":
		return "fallback"
	default:
		return "round_robin"
	}
}

// Mode returns the active routing mode ("round_robin" or "fallback").
func (p *RelayPool) Mode() string {
	if p.mode == "" {
		return "round_robin"
	}
	return p.mode
}

// Size returns the number of relays in the pool.
func (p *RelayPool) Size() int { return len(p.relays) }

// Bases returns the relay base URLs, for logging/status.
func (p *RelayPool) Bases() []string {
	out := make([]string, len(p.relays))
	for i, r := range p.relays {
		out[i] = r.Base
	}
	return out
}

// Forward relays one client request through the pool. Depending on pool mode:
// - "round_robin": requests rotate across relays; on failure, tries next.
// - "fallback": requests always start at relay 0 (primary); only falls back to relay 1, 2... on failure.
func (p *RelayPool) Forward(w http.ResponseWriter, clientReq *http.Request, scheme, host, targetPath string) {
	if len(p.relays) == 0 {
		http.Error(w, "relay-bridge: no relays configured", http.StatusBadGateway)
		return
	}

	// Buffer the body once so it can be re-sent unchanged on failover.
	var body []byte
	var err error
	if clientReq.Body != nil {
		body, err = io.ReadAll(clientReq.Body)
		_ = clientReq.Body.Close()
		if err != nil {
			http.Error(w, "relay-bridge: read request body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	start := 0
	if p.Mode() == "round_robin" {
		n := p.rr.Add(1) - 1
		start = int(n % uint64(len(p.relays)))
	} else {
		// "fallback": start from currently active cursor
		start = int(p.cursor.Load() % uint64(len(p.relays)))
	}

	var lastErr error
	for i := 0; i < len(p.relays); i++ {
		idx := (start + i) % len(p.relays)
		r := p.relays[idx]
		if verbose {
			log.Printf("relay: [%d/%d] %s <- %s %s://%s%s (mode=%s)", idx+1, len(p.relays), r.Base, clientReq.Method, scheme, host, targetPath, p.Mode())
		}
		resp, err := r.roundTrip(clientReq.Context(), clientReq.Method, body, clientReq.Header, scheme, host, targetPath)
		if err != nil {
			if verbose {
				log.Printf("relay: %s failed (%v), trying next", r.Base, err)
			}
			lastErr = err
			continue
		}

		// Failover on upstream HTTP 403 (Abuse/Blocked) and 429 (Rate Limit):
		// If a serverless relay node hits an IP-level abuse flag or rate limit from upstream,
		// don't pass the 403/429 error directly to the client — failover immediately to the next relay!
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			respSnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			log.Printf("relay: %s returned HTTP %d (%s), failing over to next relay", r.Base, resp.StatusCode, string(respSnippet))
			lastErr = fmt.Errorf("relay %s returned HTTP %d: %s", r.Base, resp.StatusCode, string(respSnippet))
			continue
		}

		// On successful response in fallback mode, update cursor to this working relay
		// so future requests go directly to this healthy node without hitting broken ones.
		if p.Mode() == "fallback" && i > 0 {
			p.cursor.Store(uint64(idx))
			log.Printf("relay: active fallback cursor shifted to [%d] %s", idx+1, r.Base)
		}

		defer resp.Body.Close()
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		flushCopy(w, resp.Body)
		return
	}
	http.Error(w, "relay-bridge: all relays failed: "+lastErr.Error(), http.StatusBadGateway)
}
