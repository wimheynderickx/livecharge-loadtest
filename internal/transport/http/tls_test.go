package http

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"livecharge/loadtest/internal/config"
)

func TestBuildTLSConfig_NilReturnsNil(t *testing.T) {
	got, err := buildTLSConfig(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("nil cfg should return nil *tls.Config; got %v", got)
	}
}

func TestBuildTLSConfig_FlagsPropagate(t *testing.T) {
	cfg := &config.TLSConfig{
		InsecureSkipVerify: true,
		ServerName:         "real.example.com",
	}
	got, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !got.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should propagate")
	}
	if got.ServerName != "real.example.com" {
		t.Errorf("ServerName = %q", got.ServerName)
	}
	if got.RootCAs != nil {
		t.Error("RootCAs should be nil when no CAFile is set")
	}
}

func TestBuildTLSConfig_ValidCAFile(t *testing.T) {
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	writeValidCAPEM(t, caPath)

	cfg := &config.TLSConfig{CAFile: caPath}
	got, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.RootCAs == nil {
		t.Fatal("RootCAs should be populated when CAFile is set")
	}
	// Sanity: the pool should contain at least our injected cert. We can't
	// inspect contents directly, but Subjects() length confirms a non-empty
	// pool. Note: Subjects is deprecated on system roots but works here
	// because we built a fresh pool when SystemCertPool failed or for
	// per-test isolation.
	if len(got.RootCAs.Subjects()) == 0 {
		t.Error("RootCAs is empty after AppendCertsFromPEM")
	}
}

func TestBuildTLSConfig_MissingCAFile(t *testing.T) {
	cfg := &config.TLSConfig{CAFile: "/tmp/loadtest-does-not-exist.pem"}
	_, err := buildTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
	if !strings.Contains(err.Error(), "read ca_file") {
		t.Errorf("error = %q, want containing 'read ca_file'", err.Error())
	}
}

func TestBuildTLSConfig_GarbageCAFile(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("not a PEM block, just words"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.TLSConfig{CAFile: junk}
	_, err := buildTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for non-PEM content")
	}
	if !strings.Contains(err.Error(), "no certificates parsed") {
		t.Errorf("error = %q, want containing 'no certificates parsed'", err.Error())
	}
}

// writeValidCAPEM emits a fresh self-signed CA cert in PEM form so the
// AppendCertsFromPEM call inside buildTLSConfig actually accepts a cert.
func writeValidCAPEM(t *testing.T, path string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "loadtest-test-CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
}
