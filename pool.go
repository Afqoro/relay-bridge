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
// both mixed). Incoming requests are round-robined across the relays, and a
// transport failure on one relay falls over to the next.
type RelayPool struct {
	relays []*Relay
	rr     atomic.Uint64
}

// NewPool builds a pool from relay base URLs. Empty entries are skipped.
func NewPool(bases []string) *RelayPool {
	return NewPoolWithSecret(bases, "")
}

// NewPoolWithSecret builds a pool from relay base URLs, attaching the secret to
// every relay. Empty entries are skipped.
func NewPoolWithSecret(bases []string, secret string) *RelayPool {
	p := &RelayPool{}
	for _, b := range bases {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		p.relays = append(p.relays, NewRelay(b, secret))
	}
	return p
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

// Forward relays one client request through the pool. On a transport error the
// next relay is tried (round-robin + failover).
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

	n := p.rr.Add(1) - 1
	start := int(n % uint64(len(p.relays)))
	var lastErr error
	for i := 0; i < len(p.relays); i++ {
		r := p.relays[(start+i)%len(p.relays)]
		if verbose {
			log.Printf("relay: %s <- %s %s://%s%s", r.Base, clientReq.Method, scheme, host, targetPath)
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

		defer resp.Body.Close()
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		flushCopy(w, resp.Body)
		return
	}
	http.Error(w, "relay-bridge: all relays failed: "+lastErr.Error(), http.StatusBadGateway)
}
