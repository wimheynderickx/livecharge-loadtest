package http

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"livecharge/loadtest/internal/config"
)

// buildTLSConfig assembles a *tls.Config from the scenario's [transport.tls]
// block. A nil cfg returns nil (let the http stack use its defaults).
func buildTLSConfig(cfg *config.TLSConfig) (*tls.Config, error) {
	if cfg == nil {
		return nil, nil
	}
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         cfg.ServerName,
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file %s: %w", cfg.CAFile, err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates parsed from ca_file %s", cfg.CAFile)
		}
		tlsCfg.RootCAs = roots
	}
	return tlsCfg, nil
}
