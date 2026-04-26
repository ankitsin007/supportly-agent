// Package tlsconfig builds *tls.Config for the agent's outbound HTTPS calls
// per the tiered model in docs/AGENT_M1_DESIGN.md §10.6.
//
// Rungs (highest security default first):
//
//  0. Public CA (default): OS root store + Mozilla bundle. No flags.
//  1. Custom CA bundle: ADD the customer's CA to the trust set, don't replace.
//  2. SPKI pin: even if the cert is rotated, only this exact public key is
//     trusted.
//  3. Insecure: requires BOTH SkipVerify=true AND Acknowledged=true to
//     prevent accidental enable.
//
// mTLS (client cert) is independent of these rungs and configured via
// ClientCertFile + ClientKeyFile.
package tlsconfig

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Options is the input from config + flags. All fields are optional.
type Options struct {
	// CABundlePath: path to a PEM file. Contents are ADDED to the system
	// root pool, not used as a replacement.
	CABundlePath string

	// CertPin: "sha256/<base64-of-spki-hash>". When set, the server cert
	// must present a matching SPKI hash regardless of what CA signed it.
	CertPin string

	// ClientCertFile + ClientKeyFile: enable mTLS. Both must be set or
	// neither.
	ClientCertFile string
	ClientKeyFile  string

	// SkipVerify disables ALL TLS verification. Acknowledged must also be
	// true; otherwise Build returns an error.
	SkipVerify   bool
	Acknowledged bool
}

// Build returns a *tls.Config configured per Opts. Errors describe exactly
// which option combination failed validation.
func Build(opts Options) (*tls.Config, error) {
	if opts.SkipVerify && !opts.Acknowledged {
		return nil, errors.New(
			"--tls-skip-verify requires also setting --i-understand-this-is-insecure; refusing to disable TLS verification silently",
		)
	}

	if (opts.ClientCertFile != "") != (opts.ClientKeyFile != "") {
		return nil, errors.New("client_cert and client_key must be set together for mTLS")
	}

	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if opts.SkipVerify {
		cfg.InsecureSkipVerify = true // #nosec G402 — gated by Acknowledged
		return cfg, nil               // pin/CA flags are no-ops in this mode
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	if opts.CABundlePath != "" {
		pem, err := os.ReadFile(opts.CABundlePath)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle %s: %w", opts.CABundlePath, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA bundle %s contained no usable certificates", opts.CABundlePath)
		}
	}
	cfg.RootCAs = pool

	if opts.CertPin != "" {
		want, err := parseSPKIPin(opts.CertPin)
		if err != nil {
			return nil, fmt.Errorf("invalid --cert-pin: %w", err)
		}
		cfg.VerifyPeerCertificate = makePinVerifier(want)
	}

	if opts.ClientCertFile != "" {
		pair, err := tls.LoadX509KeyPair(opts.ClientCertFile, opts.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}

	return cfg, nil
}

// parseSPKIPin accepts "sha256/<base64>" and returns the raw 32-byte hash.
func parseSPKIPin(pin string) ([]byte, error) {
	const prefix = "sha256/"
	if !strings.HasPrefix(pin, prefix) {
		return nil, fmt.Errorf("must start with %q", prefix)
	}
	raw := strings.TrimPrefix(pin, prefix)
	want, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		want, err = base64.RawStdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
	}
	if len(want) != sha256.Size {
		return nil, fmt.Errorf("expected %d bytes, got %d", sha256.Size, len(want))
	}
	return want, nil
}

// makePinVerifier returns a VerifyPeerCertificate callback that fails the
// handshake unless one of the verified chains' leaf certs has a SPKI hash
// matching `want`.
func makePinVerifier(want []byte) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("cert pin: server presented no certificates")
		}
		// Hash the leaf cert's SubjectPublicKeyInfo (as in HPKP/RFC 7469).
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("cert pin: parse leaf: %w", err)
		}
		got := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		if !bytesEqual(got[:], want) {
			return fmt.Errorf("cert pin: SPKI hash mismatch (got %s)",
				"sha256/"+base64.StdEncoding.EncodeToString(got[:]))
		}
		return nil
	}
}

// bytesEqual is constant-time-ish. crypto/subtle would be overkill since
// the value being compared isn't secret — the leak risk here is "did this
// public-key pin match" which already shows in the error message.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// MustParsePEMCertForTesting is a test helper that panics on bad input.
// Returned only for use in tests; not exported in package docs.
//
//nolint:unused // exported via test files
func MustParsePEMCertForTesting(p []byte) *x509.Certificate {
	block, _ := pem.Decode(p)
	if block == nil {
		panic("no PEM data")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		panic(err)
	}
	return c
}
