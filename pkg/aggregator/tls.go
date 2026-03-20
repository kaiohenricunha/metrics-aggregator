package aggregator

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// BuildTLSConfig constructs a *tls.Config from environment variables.
// Reads METRICS_SCRAPE_TLS_CACERT, METRICS_SCRAPE_TLS_CERT, METRICS_SCRAPE_TLS_KEY,
// METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY.
func BuildTLSConfig() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	caCert := strings.TrimSpace(os.Getenv("METRICS_SCRAPE_TLS_CACERT"))
	certFile := strings.TrimSpace(os.Getenv("METRICS_SCRAPE_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("METRICS_SCRAPE_TLS_KEY"))
	skipVerify := strings.TrimSpace(os.Getenv("METRICS_SCRAPE_TLS_INSECURE_SKIP_VERIFY"))

	if caCert != "" {
		pem, err := os.ReadFile(caCert)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert %q: %w", caCert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("failed to parse CA cert %q", caCert)
		}
		cfg.RootCAs = pool
	}

	if (certFile == "") != (keyFile == "") {
		return nil, fmt.Errorf("METRICS_SCRAPE_TLS_CERT and METRICS_SCRAPE_TLS_KEY must both be set or both be empty")
	}
	if certFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("loading TLS cert/key pair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	if skipVerify == "true" {
		cfg.InsecureSkipVerify = true // #nosec G402 -- controlled by explicit operator opt-in
	}

	return cfg, nil
}
