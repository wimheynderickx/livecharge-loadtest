package mockserver

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"
)

func TestGenerateSelfSignedCert_Parses(t *testing.T) {
	pair, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(pair.Certificate) == 0 || pair.PrivateKey == nil {
		t.Fatal("empty cert/key pair")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if leaf.Subject.CommonName != "loadtest-mock" {
		t.Errorf("CN = %q, want loadtest-mock", leaf.Subject.CommonName)
	}
}

func TestGenerateSelfSignedCert_SANs(t *testing.T) {
	pair, _ := GenerateSelfSignedCert()
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])

	wantDNS := map[string]bool{"localhost": false, "loadtest-mock": false}
	for _, n := range leaf.DNSNames {
		if _, ok := wantDNS[n]; ok {
			wantDNS[n] = true
		}
	}
	for n, ok := range wantDNS {
		if !ok {
			t.Errorf("missing SAN DNSName %q", n)
		}
	}
	if len(leaf.IPAddresses) < 2 {
		t.Errorf("expected SAN IPAddresses for 127.0.0.1 and ::1; got %v", leaf.IPAddresses)
	}
}

func TestGenerateSelfSignedCert_Validity(t *testing.T) {
	pair, _ := GenerateSelfSignedCert()
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	now := time.Now()
	if leaf.NotBefore.After(now) {
		t.Errorf("NotBefore is in the future: %v", leaf.NotBefore)
	}
	if leaf.NotAfter.Sub(now) > 25*time.Hour {
		t.Errorf("NotAfter further than 25h: %v", leaf.NotAfter)
	}
	if leaf.NotAfter.Sub(now) < 23*time.Hour {
		t.Errorf("NotAfter sooner than 23h: %v", leaf.NotAfter)
	}
}

func TestGenerateSelfSignedCert_TLSConfigUsable(t *testing.T) {
	pair, _ := GenerateSelfSignedCert()
	_ = &tls.Config{Certificates: []tls.Certificate{pair}}
}
