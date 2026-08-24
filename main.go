package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
)

func main() {
	relayBase := flag.String("relay", "", "relay base URL (scheme://host), required; also reads $RELAY_URL")
	secret := flag.String("secret", "", "optional x-relay-secret sent to the relay; also reads $RELAY_SECRET")
	httpListen := flag.String("http", "127.0.0.1:8080", "HTTP proxy listen address (empty disables)")
	socksListen := flag.String("socks", "127.0.0.1:1080", "SOCKS5 listen address (empty disables)")
	flag.Parse()

	if *relayBase == "" {
		*relayBase = os.Getenv("RELAY_URL")
	}
	if *secret == "" {
		*secret = os.Getenv("RELAY_SECRET")
	}
	if *relayBase == "" {
		fmt.Fprintln(os.Stderr, "usage: relay-bridge -relay https://relay-...vercel.app [-secret x] [-http :8080] [-socks :1080]")
		os.Exit(2)
	}
	if *httpListen == "" && *socksListen == "" {
		fmt.Fprintln(os.Stderr, "error: at least one of -http or -socks must be enabled")
		os.Exit(2)
	}

	relay := NewRelay(*relayBase, *secret)

	if *httpListen != "" {
		ln, err := net.Listen("tcp", *httpListen)
		if err != nil {
			log.Fatalf("http: listen %s: %v", *httpListen, err)
		}
		go func() {
			log.Printf("HTTP proxy listening on http://%s  ->  relay %s", *httpListen, *relayBase)
			if err := http.Serve(ln, &httpProxyHandler{relay: relay}); err != nil {
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
