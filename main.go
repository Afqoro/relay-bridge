package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	relayBase := flag.String("relay", "", "relay base URL (scheme://host), required; also reads $RELAY_URL")
	secret := flag.String("secret", "", "optional x-relay-secret sent to the relay; also reads $RELAY_SECRET")
	httpListen := flag.String("http", "127.0.0.1:8080", "HTTP proxy listen address (empty disables)")
	socksListen := flag.String("socks", "127.0.0.1:1080", "SOCKS5 listen address (empty disables)")
	mitm := flag.Bool("mitm", false, "enable MITM HTTPS interception so browsers can load https:// via the relay (trust the CA in the client first)")
	certDir := flag.String("certs", "", "directory for the MITM CA + leaf cache (default ~/.relay-bridge)")
	flag.Parse()

	if *relayBase == "" {
		*relayBase = os.Getenv("RELAY_URL")
	}
	if *secret == "" {
		*secret = os.Getenv("RELAY_SECRET")
	}
	if *relayBase == "" {
		fmt.Fprintln(os.Stderr, "usage: relay-bridge -relay https://relay-...vercel.app [-secret x] [-http :8080] [-socks :1080] [-mitm] [-certs DIR]")
		os.Exit(2)
	}
	if *httpListen == "" && *socksListen == "" {
		fmt.Fprintln(os.Stderr, "error: at least one of -http or -socks must be enabled")
		os.Exit(2)
	}

	relay := NewRelay(*relayBase, *secret)

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
			log.Printf("HTTP proxy listening on http://%s  ->  relay %s", *httpListen, *relayBase)
			if err := http.Serve(ln, &httpProxyHandler{relay: relay, mitm: mitmObj}); err != nil {
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
			log.Printf("SOCKS5 proxy listening on socks5://%s  ->  relay %s", *socksListen, *relayBase)
			serveSOCKS5(ln, relay)
		}()
	}

	// block forever
	select {}
}

// defaultCertDir returns the default directory for the MITM CA + leaf cache.
func defaultCertDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".relay-bridge"
	}
	return filepath.Join(home, ".relay-bridge")
}
