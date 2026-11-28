package stepca

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"encoding/json/v2"
	"encoding/pem"
	"fmt"
	"slices"
)

type renewalResponse struct {
	Certificate string   `json:"crt"`
	CA          string   `json:"ca"`
	Chain       []string `json:"certChain"` //nolint:tagliatelle // step-ca uses this exact wire field.
}

func decodeRenewal(data []byte, old *x509.Certificate, previous [][]byte, roots *x509.CertPool) ([]byte, error) {
	var response renewalResponse
	if err := json.Unmarshal(data, &response); err != nil {
		// CA-controlled JSON diagnostics can include sensitive response values.
		return nil, fmt.Errorf("decode CA renewal JSON: %w", ErrRenewal)
	}
	chain, err := response.certificateChain()
	if err != nil {
		return nil, err
	}
	leaf, err := verifyChain(chain, roots)
	if err != nil {
		return nil, fmt.Errorf("verify renewed certificate: %w", err)
	}
	if !sameCertificateIdentity(old, leaf) {
		return nil, fmt.Errorf("renewal changed key, identity or usage: %w", ErrRenewal)
	}
	if slices.EqualFunc(chain, previous, bytes.Equal) {
		return nil, nil
	}
	if !leaf.NotAfter.After(old.NotAfter) {
		return nil, fmt.Errorf("renewal did not advance expiry: %w", ErrRenewal)
	}
	var result []byte
	for _, der := range chain {
		result = append(result, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return result, nil
}

func (r renewalResponse) certificateChain() ([][]byte, error) {
	if len(r.Chain) == 0 || len(r.Chain) > maxChainLength {
		return nil, fmt.Errorf("invalid renewal chain length: %w", ErrRenewal)
	}
	chain := make([][]byte, len(r.Chain))
	for index, encoded := range r.Chain {
		der, err := decodeCertificate(encoded)
		if err != nil {
			return nil, err
		}
		chain[index] = der
	}
	if err := matchingCertificate(r.Certificate, chain[0]); err != nil {
		return nil, err
	}
	if r.CA != "" && (len(chain) < 2 || matchingCertificate(r.CA, chain[1]) != nil) {
		return nil, fmt.Errorf("CA field differs from renewal chain: %w", ErrRenewal)
	}
	return chain, nil
}

func decodeCertificate(encoded string) ([]byte, error) {
	blocks, err := certificateBlocks([]byte(encoded), 1)
	if err != nil {
		return nil, err
	}
	return blocks[0], nil
}

func certificateBlocks(data []byte, limit int) ([][]byte, error) {
	const begin, end = "-----BEGIN CERTIFICATE-----", "-----END CERTIFICATE-----"
	var blocks [][]byte
	for len(bytes.TrimSpace(data)) != 0 {
		data = bytes.TrimSpace(data)
		if len(blocks) == limit || !bytes.HasPrefix(data, []byte(begin)) {
			return nil, fmt.Errorf("certificate-only PEM bundle required within limit: %w", ErrRenewal)
		}
		boundary := bytes.Index(data, []byte(end))
		if boundary < 0 {
			return nil, fmt.Errorf("unterminated certificate PEM: %w", ErrRenewal)
		}
		boundary += len(end)
		// pem.Decode otherwise skips malformed blocks and scans for a later BEGIN.
		if bytes.Count(data[:boundary], []byte(begin)) != 1 {
			return nil, fmt.Errorf("nested certificate PEM: %w", ErrRenewal)
		}
		block, rest := pem.Decode(data[:boundary])
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(rest) != 0 {
			return nil, fmt.Errorf("invalid certificate PEM: %w", ErrRenewal)
		}
		blocks, data = append(blocks, block.Bytes), data[boundary:]
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("empty certificate PEM bundle: %w", ErrRenewal)
	}
	return blocks, nil
}

func matchingCertificate(encoded string, expected []byte) error {
	der, err := decodeCertificate(encoded)
	if err != nil || !bytes.Equal(der, expected) {
		return fmt.Errorf("redundant renewal certificate differs from chain: %w", ErrRenewal)
	}
	return nil
}

func verifyChain(chain [][]byte, roots *x509.CertPool) (*x509.Certificate, error) {
	if len(chain) == 0 || len(chain) > maxChainLength {
		return nil, fmt.Errorf("invalid certificate chain length: %w", ErrRenewal)
	}
	certs := make([]*x509.Certificate, len(chain))
	for index, der := range chain {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse renewal certificate: %w", ErrRenewal)
		}
		certs[index] = cert
	}
	if certs[0].IsCA {
		return nil, fmt.Errorf("renewal requires a non-CA leaf: %w", ErrRenewal)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	usages := certs[0].ExtKeyUsage
	if len(usages) == 0 || slices.Contains(usages, x509.ExtKeyUsageAny) {
		usages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	}
	// Verify accepts usages as alternatives; each preserved purpose must work.
	for _, usage := range usages {
		if _, err := certs[0].Verify(x509.VerifyOptions{
			Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{usage},
		}); err != nil {
			return nil, fmt.Errorf("certificate chain is not currently trusted for preserved usage: %w", ErrRenewal)
		}
	}
	return certs[0], nil
}

func sameCertificateIdentity(old, next *x509.Certificate) bool {
	return bytes.Equal(old.RawSubjectPublicKeyInfo, next.RawSubjectPublicKeyInfo) &&
		bytes.Equal(old.RawSubject, next.RawSubject) && bytes.Equal(certificateSAN(old), certificateSAN(next)) &&
		old.KeyUsage == next.KeyUsage && slices.Equal(old.ExtKeyUsage, next.ExtKeyUsage) &&
		slices.EqualFunc(old.UnknownExtKeyUsage, next.UnknownExtKeyUsage, asn1.ObjectIdentifier.Equal)
}

func certificateSAN(cert *x509.Certificate) []byte {
	for _, extension := range cert.Extensions {
		if extension.Id.Equal(asn1.ObjectIdentifier{2, 5, 29, 17}) {
			return extension.Value
		}
	}
	return nil
}
