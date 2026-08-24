package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MITM provides a dynamically-signed leaf certificate for any host, backed by a
// locally-generated CA. Install the CA cert (mitm-ca.crt) in the client/browser
// trust store to make HTTPS browsing work through the HTTP-only relay.
//
// The relay can only fetch HTTP(S) URLs server-side, so the client-side TLS
// tunnel is terminated here (MITM) and the decrypted HTTP request is forwarded
// to the relay as an absolute-form https URL. This is the only way to make a
// browser load HTTPS sites through an HTTP-only relay.
type MITM struct {
	caCert   *x509.Certificate
	caKey    *ecdsa.PrivateKey
	certFile string
	keyFile  string
	leafTTL  time.Duration

	mu    sync.Mutex
	cache map[string]*cachedCert
}

type cachedCert struct {
	cert *tls.Certificate
	exp  time.Time
}

// NewMITM loads the CA from dir (creating it if absent) and returns a MITM.
// leafTTL <= 0 uses the default (30 days).
func NewMITM(dir string, leafTTL time.Duration) (*MITM, error) {
	if dir == "" {
		dir = "."
	}
	if leafTTL <= 0 {
		leafTTL = 30 * 24 * time.Hour
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mitm: mkdir: %w", err)
	}
	certFile := filepath.Join(dir, "mitm-ca.crt")
	keyFile := filepath.Join(dir, "mitm-ca.key")

	m := &MITM{certFile: certFile, keyFile: keyFile, cache: map[string]*cachedCert{}, leafTTL: leafTTL}

	if _, err := os.Stat(certFile); err == nil {
		if _, err2 := os.Stat(keyFile); err2 == nil {
			if err = m.loadCA(); err == nil {
				return m, nil
			}
			return nil, fmt.Errorf("mitm: load CA: %w", err)
		}
	}
	if err := m.generateCA(); err != nil {
		return nil, fmt.Errorf("mitm: generate CA: %w", err)
	}
	return m, nil
}

func (m *MITM) generateCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "relay-bridge MITM CA",
			Organization: []string{"relay-bridge"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	m.caCert, _ = x509.ParseCertificate(der)
	m.caKey = key

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(m.certFile, certPEM, 0o644); err != nil {
		return err
	}
	return os.WriteFile(m.keyFile, keyPEM, 0o600)
}

func (m *MITM) loadCA() error {
	certPEM, err := os.ReadFile(m.certFile)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(m.keyFile)
	if err != nil {
		return err
	}
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return fmt.Errorf("bad cert PEM")
	}
	if m.caCert, err = x509.ParseCertificate(cb.Bytes); err != nil {
		return err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return fmt.Errorf("bad key PEM")
	}
	k, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return err
	}
	m.caKey = k
	return nil
}

// CertForHost returns a cached or freshly-signed leaf certificate for name.
func (m *MITM) CertForHost(name string) (*tls.Certificate, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		name = "localhost"
	}
	m.mu.Lock()
	if c, ok := m.cache[name]; ok && time.Now().Before(c.exp) {
		m.mu.Unlock()
		return c.cert, nil
	}
	m.mu.Unlock()

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(m.leafTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.caCert, &leafKey.PublicKey, m.caKey)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  leafKey,
	}
	m.mu.Lock()
	m.cache[name] = &cachedCert{cert: cert, exp: time.Now().Add(m.leafTTL)}
	m.mu.Unlock()
	return cert, nil
}

// TLSConfigForHost returns a *tls.Config that signs a leaf for the SNI host,
// falling back to `fallback` when the ClientHello has no SNI.
func (m *MITM) TLSConfigForHost(fallback string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Force HTTP/1.1 so the decrypted stream is parseable by
		// http.ReadRequest (we do not implement an HTTP/2 server here).
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = fallback
			}
			return m.CertForHost(name)
		},
	}
}

// CACertPath returns the path to the CA certificate to trust in the client.
func (m *MITM) CACertPath() string { return m.certFile }

// Fingerprint returns the SHA-256 fingerprint of the CA certificate (hex, colon
// separated), useful for the user to verify which CA to trust.
func (m *MITM) Fingerprint() string {
	f := sha256.Sum256(m.caCert.Raw)
	hexstr := hex.EncodeToString(f[:])
	var parts []string
	for i := 0; i < len(hexstr); i += 2 {
		parts = append(parts, strings.ToUpper(hexstr[i:i+2]))
	}
	return strings.Join(parts, ":")
}
