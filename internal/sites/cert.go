package sites

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// CertInfo describes an installed certificate.
type CertInfo struct {
	NotAfter time.Time `json:"not_after"`
	Issuer   string    `json:"issuer"`
	Names    []string  `json:"names"`
	DaysLeft int       `json:"days_left"`
}

// ParseCertFile reads the first certificate in a PEM file.
func ParseCertFile(path string, now time.Time) (*CertInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return nil, errors.New("no certificate found")
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return nil, err
		}
		return &CertInfo{
			NotAfter: c.NotAfter.UTC(),
			Issuer:   c.Issuer.CommonName,
			Names:    c.DNSNames,
			DaysLeft: int(c.NotAfter.Sub(now).Hours() / 24),
		}, nil
	}
}

// CertInfo returns the live Let's Encrypt certificate for a domain.
func (m *Manager) CertInfo(domain string) (*CertInfo, error) {
	if _, err := NormalizeDomain(domain); err != nil {
		return nil, err
	}
	return ParseCertFile(filepath.Join(m.LetsEncryptDir, "live", domain, "fullchain.pem"), time.Now())
}
