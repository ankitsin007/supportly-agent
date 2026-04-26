package tlsconfig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genSelfSignedCert returns a self-signed cert + key for use in TLS tests.
func genSelfSignedCert(t *testing.T, dnsNames []string) (certPEM, keyPEM []byte, leaf *x509.Certificate) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	leaf, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certPEM, keyPEM, leaf
}

func TestBuild_DefaultUsesSystemPool(t *testing.T) {
	cfg, err := Build(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs == nil {
		t.Errorf("RootCAs should be the system pool, got nil")
	}
	if cfg.InsecureSkipVerify {
		t.Errorf("default should NOT skip verify")
	}
}

func TestBuild_CustomCABundleAddsTrust(t *testing.T) {
	dir := t.TempDir()
	certPEM, _, _ := genSelfSignedCert(t, []string{"example.com"})
	bundlePath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(bundlePath, certPEM, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Build(Options{CABundlePath: bundlePath})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("RootCAs should include the custom CA")
	}
}

func TestBuild_BadCABundleErrors(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(bundlePath, []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Options{CABundlePath: bundlePath}); err == nil {
		t.Error("expected error for non-PEM bundle")
	}
}

func TestBuild_SkipVerifyRequiresAcknowledgement(t *testing.T) {
	if _, err := Build(Options{SkipVerify: true}); err == nil {
		t.Error("expected error when SkipVerify set without Acknowledged")
	}
	cfg, err := Build(Options{SkipVerify: true, Acknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true after acknowledgement")
	}
}

func TestBuild_MTLSRequiresBothFiles(t *testing.T) {
	if _, err := Build(Options{ClientCertFile: "/x"}); err == nil {
		t.Error("expected error when only client cert is set")
	}
	if _, err := Build(Options{ClientKeyFile: "/x"}); err == nil {
		t.Error("expected error when only client key is set")
	}
}

func TestParseSPKIPin_RoundTrip(t *testing.T) {
	hash := sha256.Sum256([]byte("anything"))
	pin := "sha256/" + base64.StdEncoding.EncodeToString(hash[:])
	got, err := parseSPKIPin(pin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(got, hash[:]) {
		t.Errorf("round trip mismatch")
	}
}

func TestParseSPKIPin_RejectsBadFormat(t *testing.T) {
	cases := []string{
		"not-a-pin",
		"sha256/short",
		"md5/abcdefgh",
	}
	for _, p := range cases {
		if _, err := parseSPKIPin(p); err == nil {
			t.Errorf("expected error for %q", p)
		}
	}
}

// startTLSServer returns a TLS test server with a self-signed cert plus
// a path to a CA bundle (the cert itself, since it's self-signed) and the
// leaf cert for SPKI extraction.
func startTLSServer(t *testing.T) (srv *httptest.Server, caBundlePath string, leaf *x509.Certificate) {
	t.Helper()
	certPEM, keyPEM, leaf := genSelfSignedCert(t, []string{"127.0.0.1", "localhost"})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	caBundlePath = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caBundlePath, certPEM, 0644); err != nil {
		t.Fatal(err)
	}
	return srv, caBundlePath, leaf
}

func TestPinVerifier_AllowsMatchingCert(t *testing.T) {
	srv, caBundle, leaf := startTLSServer(t)

	hash := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	pin := "sha256/" + base64.StdEncoding.EncodeToString(hash[:])

	cfg, err := Build(Options{CABundlePath: caBundle, CertPin: pin})
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected matching pin to succeed, got: %v", err)
	}
	resp.Body.Close()
}

func TestPinVerifier_RejectsMismatch(t *testing.T) {
	srv, caBundle, _ := startTLSServer(t)

	wrong := sha256.Sum256([]byte("wrong-key"))
	pin := "sha256/" + base64.StdEncoding.EncodeToString(wrong[:])

	cfg, err := Build(Options{CABundlePath: caBundle, CertPin: pin})
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("expected pin mismatch to fail handshake")
	}
}
