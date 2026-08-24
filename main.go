package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// verbose enables per-request logging of which relay served each request.
var verbose bool

func main() {
	relayFlag := flag.String("relay", "", "single relay base URL (scheme://host) — overrides all env pool config")
	secret := flag.String("secret", "", "optional x-relay-secret sent to the relay; also reads $RELAY_SECRET")
	httpListen := flag.String("http", "127.0.0.1:8080", "HTTP proxy listen address (empty disables)")
	socksListen := flag.String("socks", "127.0.0.1:1080", "SOCKS5 listen address (empty disables)")
	mitm := flag.Bool("mitm", false, "enable MITM HTTPS interception so browsers can load https:// via the relay (trust the CA in the client first)")
	certDir := flag.String("certs", "", "directory for the MITM CA + leaf cache (default ~/.relay-bridge)")
	flag.BoolVar(&verbose, "v", false, "verbose: log each relayed request and which relay served it")
	flag.Parse()
	loadDotEnv() // pick up ./.env after flags, before reading env vars

	if *secret == "" {
		*secret = os.Getenv("RELAY_SECRET")
	}

	pool, err := buildPool(*relayFlag, *secret)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(2)
	}
	if *httpListen == "" && *socksListen == "" {
		fmt.Fprintln(os.Stderr, "error: at least one of -http or -socks must be enabled")
		os.Exit(2)
	}

	// Log the pool config so it's obvious which type + relays are active.
	log.Printf("relay pool: %d relay(s) for %s", pool.Size(), relayTypeLabel(*relayFlag))
	for i, b := range pool.Bases() {
		log.Printf("  [%d] %s", i+1, b)
	}

	var mitmObj *MITM
	if *mitm {
		dir := *certDir
		if dir == "" {
			dir = defaultCertDir()
		}
		var err error
		mitmObj, err = NewMITM(dir, 0)
		if err != nil {
			log.Fatalf("mitm: %v", err)
		}
		log.Printf("MITM enabled. Trust this CA in your browser/OS: %s", mitmObj.CACertPath())
		log.Printf("  CA SHA-256 fingerprint: %s", mitmObj.Fingerprint())
	}

	if *httpListen != "" {
		ln, err := net.Listen("tcp", *httpListen)
		if err != nil {
			log.Fatalf("http: listen %s: %v", *httpListen, err)
		}
		go func() {
			log.Printf("HTTP proxy listening on http://%s  ->  relay pool (%d)", *httpListen, pool.Size())
			if err := http.Serve(ln, &httpProxyHandler{pool: pool, mitm: mitmObj}); err != nil {
				log.Fatalf("http: %v", err)
			}
		}()
	}

	if *socksListen != "" {
		ln, err := net.Listen("tcp", *socksListen)
		if err != nil {
			log.Fatalf("socks: listen %s: %v", *socksListen, err)
		}
		go func() {
			log.Printf("SOCKS5 proxy listening on socks5://%s  ->  relay pool (%d)", *socksListen, pool.Size())
			serveSOCKS5(ln, pool)
		}()
	}

	// block forever
	select {}
}

// loadDotEnv reads ./.env (if present) into the process environment WITHOUT
// overriding variables already set (so pm2/real env wins). Format: KEY=value,
// one per line; blank lines and # comments are ignored. Values are literal (no
// interpolation). This lets the relay list live in a file while still honoring
// env vars when running under pm2.
func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return // no .env -> rely on existing environment
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			continue
		}
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, val)
		}
	}
}

// buildPool resolves the relay pool from either the -relay flag (single relay) or
// the environment: RELAY_TYPE selects "vercel" or "cloudflare", and the matching
// RELAY_VERCEL / RELAY_CLOUDFLARE list is used. The two types are never mixed: a
// single process always uses exactly one type's list.
func buildPool(relayFlag, secret string) (*RelayPool, error) {
	if relayFlag != "" {
		return NewPoolWithSecret([]string{relayFlag}, secret), nil
	}
	rawType := os.Getenv("RELAY_TYPE")
	if rawType == "" {
		// Fall back to a single relay via RELAY_URL.
		if u := os.Getenv("RELAY_URL"); u != "" {
			return NewPoolWithSecret([]string{u}, secret), nil
		}
		return nil, fmt.Errorf("no relay configured: set -relay, RELAY_URL, or RELAY_TYPE=vercel|cloudflare with its RELAY_VERCEL/RELAY_CLOUDFLARE list")
	}
	switch relayType(rawType) {
	case "vercel":
		return newPoolFromEnv("RELAY_VERCEL", secret)
	case "cloudflare":
		return newPoolFromEnv("RELAY_CLOUDFLARE", secret)
	default:
		return nil, fmt.Errorf("invalid RELAY_TYPE %q: must be 'vercel' or 'cloudflare'", rawType)
	}
}

// relayType normalizes RELAY_TYPE: accepts "vercel"/"cloudflare" and also the
// lenient forms "relay vercel"/"relay cloudflare". Returns "" if unrecognized.
func relayType(s string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.TrimPrefix(t, "relay ")
	t = strings.TrimSpace(t)
	switch t {
	case "vercel", "cloudflare":
		return t
	}
	return ""
}

// relayTypeLabel returns a human-readable label for the active pool type.
func relayTypeLabel(relayFlag string) string {
	if relayFlag != "" {
		return "single (-relay)"
	}
	if t := relayType(os.Getenv("RELAY_TYPE")); t != "" {
		return t
	}
	return "single (RELAY_URL)"
}

func newPoolFromEnv(key, secret string) (*RelayPool, error) {
	raw := os.Getenv(key)
	p := NewPoolWithSecret(strings.Split(raw, ","), secret)
	if p.Size() == 0 {
		return nil, fmt.Errorf("env %s is empty: set a comma-separated relay list (e.g. %s=\"https://a,https://b\")", key, key)
	}
	return p, nil
}

// defaultCertDir returns the default directory for the MITM CA + leaf cache.
func defaultCertDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".relay-bridge"
	}
	return filepath.Join(home, ".relay-bridge")
}
